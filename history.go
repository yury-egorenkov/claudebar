package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"time"

	"github.com/mitranim/gg"
)

// Weekly usage over time, for the per-day strip. The API reports only the current
// percentage, so every successful fetch adds a sample here (every 2 min or less).

const (
	Day = 24 * time.Hour
	// Samples older than this are dropped on write: one week plus a margin.
	historyKeep = 8 * Day
)

type Sample struct {
	At       int64   `json:"at"`        // epoch seconds
	Used     float64 `json:"used"`      // weekly % used at that moment
	ResetsAt int64   `json:"resets_at"` // identifies the weekly window
}

func HistoryPath() string { return filepath.Join(ConfigDir(), "history.jsonl") }

func ReadHistory(path string) []Sample {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	gg.Try(err)

	var out []Sample
	scan := bufio.NewScanner(bytes.NewReader(body))
	for scan.Scan() {
		if len(bytes.TrimSpace(scan.Bytes())) > 0 {
			out = append(out, gg.JsonDecodeTo[Sample](scan.Bytes()))
		}
	}
	gg.Try(scan.Err())
	return out
}

// Rewrites the file through a temp file: old samples dropped, the new one appended.
func AppendHistory(path string, sample Sample, now time.Time) {
	var buf bytes.Buffer
	for _, old := range ReadHistory(path) {
		if now.Sub(time.Unix(old.At, 0)) < historyKeep {
			buf.Write(gg.JsonBytes(old))
			buf.WriteByte('\n')
		}
	}
	buf.Write(gg.JsonBytes(sample))
	buf.WriteByte('\n')

	gg.Try(os.MkdirAll(filepath.Dir(path), 0o700))
	tmp := path + ".tmp"
	gg.Try(os.WriteFile(tmp, buf.Bytes(), 0o600))
	gg.Try(os.Rename(tmp, path))
}

type DayState int

const (
	DayPast DayState = iota
	DayToday
	DayFuture
)

var dayStateNames = [...]string{"past", "today", "future"}

func (self DayState) MarshalText() ([]byte, error) { return []byte(dayStateNames[self]), nil }

// One day of the weekly window: 24 h counted from the reset time, not from midnight,
// so every day has the same budget, 100/7 % of the week.
type DayView struct {
	Start int64    `json:"start"` // epoch seconds
	State DayState `json:"state"`
	Spent float64  `json:"spent"` // % of the week spent that day
	Known bool     `json:"known"` // false: no samples cover the day (before the first sample)
}

// The current moment counts as a sample, so today is up to date even between fetches.
func WeekDays(history []Sample, week Window, now time.Time) []DayView {
	if !week.Live(now) {
		return []DayView{}
	}
	start := week.Reset().Add(-Week)

	var samples []Sample
	for _, sample := range history {
		if sample.ResetsAt == week.ResetsAt && sample.At <= now.Unix() {
			samples = append(samples, sample)
		}
	}
	samples = append(samples, Sample{At: now.Unix(), Used: week.Used, ResetsAt: week.ResetsAt})

	out := make([]DayView, 7)
	for ind := range out {
		begin := start.Add(time.Duration(ind) * Day)
		end := begin.Add(Day)
		day := DayView{Start: begin.Unix()}

		switch {
		case !now.Before(end):
			day.State = DayPast
		case !now.Before(begin):
			day.State = DayToday
			end = now
		default:
			day.State = DayFuture
		}

		if day.State != DayFuture {
			from, okFrom := usedAt(samples, start, begin)
			to, okTo := usedAt(samples, start, end)
			day.Known = okFrom && okTo
			if day.Known {
				day.Spent = max(0, to-from)
			}
		}
		out[ind] = day
	}
	return out
}

// Weekly % used at `at`: 0 at the window start (the reset), otherwise the last sample
// at or before `at`. Unknown before the first sample.
func usedAt(samples []Sample, start time.Time, at time.Time) (float64, bool) {
	if !at.After(start) {
		return 0, true
	}
	used, ok := 0.0, false
	for _, sample := range samples {
		if sample.At <= at.Unix() {
			used, ok = sample.Used, true
		}
	}
	return used, ok
}
