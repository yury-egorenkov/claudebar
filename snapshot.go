package main

import (
	"math"
	"time"
)

// What the app (ClaudeBar, SwiftUI) shows: `claudebar json`. The app only renders it;
// all math lives here and is covered by tests.
type Snapshot struct {
	Connected bool        `json:"connected"`
	UpdatedAt int64       `json:"updated_at"` // epoch seconds, 0: never
	Level     Level       `json:"level"`      // weekly pace and five-hour window combined
	Badge     string      `json:"badge"`      // menu bar text: weekly % left or "–"
	Week      View        `json:"week"`
	FiveHour  View        `json:"five_hour"`
	Models    []ModelView `json:"models"` // per-model weekly limits, e.g. Fable
	Days      []DayView   `json:"days"`   // the 7 days of the weekly window, spend per day
	Error     string      `json:"error"`  // last usage API error; data may still come from the status line
}

type ModelView struct {
	Name     string  `json:"name"`
	Left     float64 `json:"left"`
	ResetsAt int64   `json:"resets_at"`
}

// One limit window. All percentages are what is LEFT, 0–100.
type View struct {
	Has      bool    `json:"has"`
	Left     float64 `json:"left"`
	EvenLeft float64 `json:"even_left"` // what would be left at an even pace
	ResetsAt int64   `json:"resets_at"`
	RunOutAt int64   `json:"run_out_at"` // 0: lasts until the reset
	PerDay   float64 `json:"per_day"`    // weekly only: % per day to last until the reset
	Level    Level   `json:"level"`
}

func MakeSnapshot(state State, connected bool, now time.Time) Snapshot {
	lim := state.Limits
	bar := MenuBar(lim, now)

	out := Snapshot{
		Connected: connected,
		Level:     bar.Level,
		Badge:     bar.Title,
		Week:      makeView(WeekPace(lim.SevenDay, now)),
		FiveHour:  makeView(WindowPace(lim.FiveHour, FiveHour, now)),
	}
	if bar.Level == LevelUnknown {
		out.Badge = "–"
	}
	out.Models = []ModelView{}
	out.Days = []DayView{}
	for _, model := range state.Models {
		if model.Window.Live(now) {
			out.Models = append(out.Models, ModelView{Name: model.Name, Left: math.Max(0, 100-model.Window.Used), ResetsAt: model.Window.ResetsAt})
		}
	}
	out.FiveHour.Level = FiveHourLevel(lim.FiveHour, now)
	out.FiveHour.PerDay = 0
	if !state.CapturedAt.IsZero() {
		out.UpdatedAt = state.CapturedAt.Unix()
	}
	return out
}

func makeView(pace Pace) View {
	if pace.Level == LevelUnknown {
		return View{}
	}
	out := View{
		Has:      true,
		Left:     pace.Left(),
		EvenLeft: 100 - pace.Even,
		ResetsAt: pace.Reset.Unix(),
		PerDay:   pace.PerDay,
		Level:    pace.Level,
	}
	if !pace.RunOut.IsZero() {
		out.RunOutAt = pace.RunOut.Unix()
	}
	return out
}
