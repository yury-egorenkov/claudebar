package main

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/mitranim/gg"
	"github.com/mitranim/gg/gtest"
)

// Reset on Saturday 10:00 UTC; the week started the previous Saturday 10:00.
var testReset = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

func testWeek(used float64) Window { return Window{Used: used, ResetsAt: testReset.Unix()} }

// The moment when fraction `frac` of the week has passed.
func testAt(frac float64) time.Time {
	return testReset.Add(-Week).Add(time.Duration(frac * float64(Week)))
}

func TestWeekPace_even(t *testing.T) {
	defer gtest.Catch(t)

	pace := WeekPace(testWeek(50), testAt(0.5))
	gtest.Eq(pace.Even, 50.0)
	gtest.Eq(pace.Over, 0.0)
	gtest.Eq(pace.Level, LevelOk)
	gtest.True(pace.RunOut.IsZero())
	gtest.Eq(math.Round(pace.PerDay*100), 1429.0)
}

func TestWeekPace_fast(t *testing.T) {
	defer gtest.Catch(t)

	// Half the limit in a quarter of the week: runs out by mid-week.
	pace := WeekPace(testWeek(50), testAt(0.25))
	gtest.Eq(pace.Over, 25.0)
	gtest.Eq(pace.Level, LevelBad)
	gtest.Eq(pace.RunOut, testAt(0.5))
}

// Half the week passed (Wed 22:00); the reset is Sat 10:00.
func TestWeekPace_levels(t *testing.T) {
	defer gtest.Catch(t)

	now := testAt(0.5)
	// 2 pp ahead: within the margin, no forecast.
	gtest.Eq(WeekPace(testWeek(52), now).Level, LevelOk)
	// Runs out 15 h before the reset.
	gtest.Eq(WeekPace(testWeek(55), now).Level, LevelSoon)
	// 28 h before.
	gtest.Eq(WeekPace(testWeek(60), now).Level, LevelWarn)
	gtest.Eq(WeekPace(testWeek(60), now).RunOut, testReset.Add(-28*time.Hour))
	// 56 h before.
	gtest.Eq(WeekPace(testWeek(75), now).Level, LevelBad)
	// Behind pace, but under 10% left.
	gtest.Eq(WeekPace(testWeek(92), testAt(0.95)).Level, LevelBad)
}

func TestWeekPace_spent(t *testing.T) {
	defer gtest.Catch(t)

	now := testAt(0.9)
	pace := WeekPace(testWeek(100), now)
	gtest.Eq(pace.Level, LevelBad)
	gtest.Eq(pace.RunOut, now)
	gtest.Eq(pace.PerDay, 0.0)
}

func TestWeekPace_unknown(t *testing.T) {
	defer gtest.Catch(t)

	gtest.Eq(WeekPace(Window{}, testAt(0.5)).Level, LevelUnknown)
	gtest.Eq(WeekPace(testWeek(30), testReset.Add(time.Minute)).Level, LevelUnknown)
}

func TestMenuBar(t *testing.T) {
	defer gtest.Catch(t)

	gtest.Eq(MenuBar(Limits{SevenDay: testWeek(50)}, testAt(0.5)), Bar{Left: 50, Level: LevelOk, Title: "50%"})
	gtest.Eq(MenuBar(Limits{SevenDay: testWeek(75)}, testAt(0.5)), Bar{Left: 25, Level: LevelBad, Title: "25%"})
	gtest.Eq(MenuBar(Limits{}, testAt(0.5)), Bar{Level: LevelUnknown, Title: "—"})

	// The week is fine, but the five-hour window is nearly used up.
	five := Window{Used: 92, ResetsAt: testAt(0.51).Unix()}
	gtest.Eq(MenuBar(Limits{SevenDay: testWeek(50), FiveHour: five}, testAt(0.5)).Level, LevelBad)
}

func TestDetails(t *testing.T) {
	defer gtest.Catch(t)

	now := testAt(0.25) // Sat 14.11 + 42 h = Mon 16.11 04:00 UTC
	gtest.Equal(
		Details(Limits{SevenDay: testWeek(50)}, now.Add(-5*time.Minute), now.In(time.UTC)),
		[]string{
			"Week: 50% left · even pace 75%",
			"At this pace it runs out: Tue 22:00",
			"Reset: Sat 10:00 · budget 10% per day",
			"Updated: 03:55 (5 min ago)",
		},
	)
}

func TestMergeLimits(t *testing.T) {
	defer gtest.Catch(t)

	now := testAt(0.5)
	old := Window{Used: 10, ResetsAt: now.Add(-time.Hour).Unix()}
	live := Window{Used: 20, ResetsAt: now.Add(time.Hour).Unix()}

	gtest.Eq(MergeLimits(Limits{FiveHour: live, SevenDay: testWeek(40)}, Limits{SevenDay: testWeek(41)}, now),
		Limits{FiveHour: live, SevenDay: testWeek(41)})
	gtest.Eq(MergeLimits(Limits{FiveHour: old}, Limits{}, now), Limits{})
}

func TestState_roundtrip(t *testing.T) {
	defer gtest.Catch(t)

	path := filepath.Join(t.TempDir(), "sub", "state.json")
	gtest.Equal(ReadState(path), State{})

	now := testAt(0.5).Truncate(time.Second)
	saveLimits(path, Limits{SevenDay: testWeek(40)}, now)
	saveLimits(path, Limits{}, now.Add(time.Minute)) // no windows: the file stays unchanged

	state := ReadState(path)
	gtest.Eq(state.Limits, Limits{SevenDay: testWeek(40)})
	gtest.True(state.CapturedAt.Equal(now))
}

func TestStatuslineText(t *testing.T) {
	defer gtest.Catch(t)

	var input StatuslineInput
	input.Model.DisplayName = "Opus 5.5"
	input.RateLimits = Limits{SevenDay: testWeek(50), FiveHour: Window{Used: 23.5, ResetsAt: testAt(0.51).Unix()}}
	gtest.Eq(statuslineText(input, testAt(0.5)), "")

	input.RateLimits.SevenDay = testWeek(60)
	gtest.Eq(statuslineText(input, testAt(0.5)), "week 40% left · runs out Fri 06:00")

	input.RateLimits.SevenDay = testWeek(75)
	input.RateLimits.FiveHour.Used = 80
	gtest.Eq(statuslineText(input, testAt(0.5)), "week 25% left · runs out Thu 02:00 · 5h 20% left · slow down")
}

func TestMakeSnapshot(t *testing.T) {
	defer gtest.Catch(t)

	now := testAt(0.25)
	five := Window{Used: 40, ResetsAt: now.Add(time.Hour).Unix()}
	state := State{Limits: Limits{SevenDay: testWeek(50), FiveHour: five}, CapturedAt: now.Add(-time.Minute)}

	gtest.Equal(MakeSnapshot(state, true, now), Snapshot{
		Connected: true,
		UpdatedAt: now.Add(-time.Minute).Unix(),
		Level:     LevelBad,
		Badge:     "50%",
		Week: View{
			Has: true, Left: 50, EvenLeft: 75, ResetsAt: testReset.Unix(),
			RunOutAt: testAt(0.5).Unix(), PerDay: 50 / (testReset.Sub(now).Hours() / 24), Level: LevelBad,
		},
		// 4 of 5 hours passed: even pace leaves 20%.
		FiveHour: View{Has: true, Left: 60, EvenLeft: 20, ResetsAt: five.ResetsAt, Level: LevelOk},
		Models:   []ModelView{},
		Days:     []DayView{},
	})

	gtest.Equal(MakeSnapshot(State{}, false, now), Snapshot{Badge: "–", Models: []ModelView{}, Days: []DayView{}})
	gtest.Eq(string(gg.JsonBytes(LevelWarn)), `"warn"`)
}

func TestNewestState(t *testing.T) {
	defer gtest.Catch(t)

	mac, box := t.TempDir(), t.TempDir()
	now := testAt(0.5).Truncate(time.Second)
	saveLimits(filepath.Join(mac, "claudebar.json"), Limits{SevenDay: testWeek(30)}, now)
	saveLimits(filepath.Join(box, "claudebar.json"), Limits{SevenDay: testWeek(35)}, now.Add(time.Minute))

	gtest.Eq(NewestState([]string{mac, box}).Limits.SevenDay.Used, 35.0)
	gtest.Eq(NewestState([]string{mac, filepath.Join(box, "missing")}).Limits.SevenDay.Used, 30.0)

	gtest.False(AnyConnected([]string{mac, box}))
	Connect(filepath.Join(box, "settings.json"), "/x/claudebar-cli")
	gtest.True(AnyConnected([]string{mac, box}))
}

func TestWeekDays(t *testing.T) {
	defer gtest.Catch(t)

	start := testReset.Add(-Week) // Sat 14.11 10:00 UTC
	at := func(hours float64, used float64) Sample {
		return Sample{At: start.Add(time.Duration(hours * float64(time.Hour))).Unix(), Used: used, ResetsAt: testReset.Unix()}
	}
	history := []Sample{
		at(10, 6),  // day 0
		at(23, 14), // day 0, last sample
		at(30, 20), // day 1
		at(47, 23), // day 1, last sample
		// day 2: the Mac was off, no samples
		at(75, 30), // day 3
		{At: start.Add(time.Hour).Unix(), Used: 99, ResetsAt: 1}, // another window: ignored
	}
	now := start.Add(76 * time.Hour) // day 3, 04:00 into it
	days := WeekDays(history, testWeek(31), now)

	gtest.Eq(len(days), 7)
	gtest.Eq(days[0], DayView{Start: start.Unix(), State: DayPast, Spent: 14, Known: true})
	gtest.Eq(days[1], DayView{Start: start.Add(Day).Unix(), State: DayPast, Spent: 9, Known: true})
	// Nothing sampled on day 2: its spend shows up on day 3.
	gtest.Eq(days[2], DayView{Start: start.Add(2 * Day).Unix(), State: DayPast, Spent: 0, Known: true})
	gtest.Eq(days[3], DayView{Start: start.Add(3 * Day).Unix(), State: DayToday, Spent: 8, Known: true})
	gtest.Eq(days[4], DayView{Start: start.Add(4 * Day).Unix(), State: DayFuture})

	// History starting mid-week: earlier days are unknown.
	late := []Sample{at(50, 23)}
	days = WeekDays(late, testWeek(31), now)
	gtest.Eq(days[0].Known, false)
	gtest.Eq(days[1].Known, false)
	// The first sample is inside day 2: its start is not covered either.
	gtest.Eq(days[2].Known, false)
	gtest.Eq(days[3], DayView{Start: start.Add(3 * Day).Unix(), State: DayToday, Spent: 8, Known: true})

	gtest.Equal(WeekDays(history, Window{}, now), []DayView{})
}

func TestAppendHistory(t *testing.T) {
	defer gtest.Catch(t)

	path := filepath.Join(t.TempDir(), "sub", "history.jsonl")
	now := testAt(0.5)
	AppendHistory(path, Sample{At: now.Add(-9 * Day).Unix(), Used: 1}, now.Add(-9*Day))
	AppendHistory(path, Sample{At: now.Add(-time.Hour).Unix(), Used: 40}, now.Add(-time.Hour))
	AppendHistory(path, Sample{At: now.Unix(), Used: 41}, now)

	// The 9-day-old sample is dropped.
	gtest.Equal(ReadHistory(path), []Sample{{At: now.Add(-time.Hour).Unix(), Used: 40}, {At: now.Unix(), Used: 41}})
}

func TestWeekPace_earlyBurst(t *testing.T) {
	defer gtest.Catch(t)

	// 4% in the first 3 hours: rate taken over a full day, lasts until the reset.
	pace := WeekPace(testWeek(4), testReset.Add(-Week).Add(3*time.Hour))
	gtest.Eq(pace.Level, LevelOk)
	gtest.True(pace.RunOut.IsZero())

	// 20% in the first 3 hours: 20% a day runs out after 99 h, 69 h early.
	pace = WeekPace(testWeek(20), testReset.Add(-Week).Add(3*time.Hour))
	gtest.Eq(pace.Level, LevelBad)
	gtest.Eq(pace.RunOut, testReset.Add(-Week).Add(99*time.Hour))
}
