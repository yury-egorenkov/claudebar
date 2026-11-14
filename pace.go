package main

import (
	"fmt"
	"math"
	"time"
)

// Limit window from the `rate_limits` field of the Claude Code status line.
// ResetsAt == 0: no window (Claude Code has not sent it yet, or dropped it after the reset).
type Window struct {
	Used     float64 `json:"used_percentage"`
	ResetsAt int64   `json:"resets_at"`
}

func (self Window) Has() bool { return self.ResetsAt > 0 }

func (self Window) Reset() time.Time { return time.Unix(self.ResetsAt, 0) }

// The window has not reset yet at `now`.
func (self Window) Live(now time.Time) bool { return self.Has() && now.Before(self.Reset()) }

type Limits struct {
	FiveHour Window `json:"five_hour"`
	SevenDay Window `json:"seven_day"`
}

type Level int

// By how early the limit runs out at the current pace, measured in parts of the window
// (for the week: one part = 1 day):
//
//	ok    lasts until the reset
//	soon  runs out up to 1/7 of the window before the reset (yellow)
//	warn  more than 1/7 before (orange)
//	bad   more than 2/7 before, or under 10% left (red)
const (
	LevelUnknown Level = iota
	LevelOk
	LevelSoon
	LevelWarn
	LevelBad
)

var levelNames = [...]string{"unknown", "ok", "soon", "warn", "bad"}

// JSON for the app: "unknown", "ok", "soon", "warn", "bad".
func (self Level) MarshalText() ([]byte, error) { return []byte(levelNames[self]), nil }

func (self Level) Mark() string {
	switch self {
	case LevelOk:
		return "🟢"
	case LevelSoon:
		return "🟡"
	case LevelWarn:
		return "🟠"
	case LevelBad:
		return "🔴"
	default:
		return "⚪"
	}
}

const (
	Week     = 7 * 24 * time.Hour
	FiveHour = 5 * time.Hour
	// Percentage points above the even pace before any forecast: right after the reset
	// 2% in the first hour would otherwise "run out" already.
	OverMargin = 2.0
	// Under this % left the level is bad whatever the pace.
	LeftBad = 10.0
)

// Spending pace of the weekly limit.
type Pace struct {
	Used  float64 // used, %
	Even  float64 // what would be used by now at an even pace until the reset, %
	Over  float64 // Used − Even, percentage points
	Reset time.Time
	// When the limit runs out at the current pace; zero: it lasts until the reset.
	RunOut time.Time
	// % per day that can be spent to last until the reset.
	PerDay float64
	Level  Level
}

func WeekPace(win Window, now time.Time) Pace { return WindowPace(win, Week, now) }

// Pace of a window of the given length. Level is by the pace; the five-hour window
// uses FiveHourLevel instead.
func WindowPace(win Window, length time.Duration, now time.Time) Pace {
	if !win.Live(now) {
		return Pace{}
	}

	reset := win.Reset()
	start := reset.Add(-length)
	elapsed := now.Sub(start)
	left := reset.Sub(now)

	out := Pace{
		Used:  win.Used,
		Even:  100 * clamp01(float64(elapsed)/float64(length)),
		Reset: reset,
	}
	out.Over = out.Used - out.Even
	out.PerDay = math.Max(0, 100-out.Used) / left.Hours() * 24

	if out.Used >= 100 {
		out.RunOut = now
	} else if out.Over > OverMargin && elapsed > 0 {
		// Current rate: Used percent per elapsed, but over at least 1/7 of the window
		// (a day of the week): 4% in the first 3 hours is not a weekly rate.
		span := max(elapsed, length/7)
		runOut := now.Add(time.Duration(float64(span) * (100 - out.Used) / out.Used))
		if runOut.Before(reset) {
			out.RunOut = runOut
		}
	}
	out.Level = paceLevel(out, length)
	return out
}

func paceLevel(pace Pace, length time.Duration) Level {
	part := length / 7
	early := time.Duration(0)
	if !pace.RunOut.IsZero() {
		early = pace.Reset.Sub(pace.RunOut)
	}
	switch {
	case pace.Left() < LeftBad || early > 2*part:
		return LevelBad
	case early > part:
		return LevelWarn
	case early > 0:
		return LevelSoon
	default:
		return LevelOk
	}
}

func (self Pace) Left() float64 { return math.Max(0, 100-self.Used) }

func FiveHourLevel(win Window, now time.Time) Level { return WindowPace(win, FiveHour, now).Level }

func clamp01(val float64) float64 { return math.Min(1, math.Max(0, val)) }

// What the menu bar shows: weekly limit left, level (weekly pace and five-hour window), text.
type Bar struct {
	Left  float64
	Level Level
	Title string
}

func MenuBar(lim Limits, now time.Time) Bar {
	pace := WeekPace(lim.SevenDay, now)
	level := max(pace.Level, FiveHourLevel(lim.FiveHour, now))
	if pace.Level == LevelUnknown {
		return Bar{Level: LevelUnknown, Title: "—"}
	}
	return Bar{Left: pace.Left(), Level: level, Title: fmt.Sprintf("%.0f%%", pace.Left())}
}

// Menu lines, top to bottom.
func Details(lim Limits, capturedAt time.Time, now time.Time) []string {
	var out []string
	pace := WeekPace(lim.SevenDay, now)

	if pace.Level == LevelUnknown {
		out = append(out, "Week: no data yet, waiting for a Claude Code reply")
	} else {
		out = append(out, fmt.Sprintf("Week: %.0f%% left · even pace %.0f%%", pace.Left(), 100-pace.Even))
		switch {
		case pace.Used >= 100:
			out = append(out, "Limit used up")
		case pace.RunOut.IsZero():
			out = append(out, "At this pace it lasts until the reset")
		default:
			out = append(out, "At this pace it runs out: "+formatWhen(pace.RunOut, now))
		}
		out = append(out, fmt.Sprintf("Reset: %s · budget %.0f%% per day", formatWhen(pace.Reset, now), pace.PerDay))
	}

	if lim.FiveHour.Live(now) {
		out = append(out, fmt.Sprintf("5 hours: %.0f%% left · reset %s", math.Max(0, 100-lim.FiveHour.Used), formatWhen(lim.FiveHour.Reset(), now)))
	}

	if !capturedAt.IsZero() {
		out = append(out, fmt.Sprintf("Updated: %s (%s ago)", capturedAt.Format("15:04"), formatAgo(now.Sub(capturedAt))))
	}
	return out
}

// "14:00" for today, otherwise "Thu 14:00".
func formatWhen(val time.Time, now time.Time) string {
	val = val.In(now.Location())
	if sameDay(val, now) {
		return val.Format("15:04")
	}
	return val.Format("Mon 15:04")
}

func sameDay(one, two time.Time) bool {
	y1, m1, d1 := one.Date()
	y2, m2, d2 := two.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

func formatAgo(val time.Duration) string {
	switch {
	case val < time.Minute:
		return "<1 min"
	case val < time.Hour:
		return fmt.Sprintf("%d min", int(val/time.Minute))
	case val < 48*time.Hour:
		return fmt.Sprintf("%d h", int(val/time.Hour))
	default:
		return fmt.Sprintf("%d d", int(val/(24*time.Hour)))
	}
}
