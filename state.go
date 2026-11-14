package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mitranim/gg"
)

// File through which the Claude Code status line passes the limits to the menu bar.
// Holds only percentages and reset times: no keys, no paths, no session text.
type State struct {
	Limits Limits `json:"rate_limits"`
	// Per-model weekly limits (e.g. Fable); only the usage API reports them.
	Models     []ModelLimit `json:"models,omitempty"`
	CapturedAt time.Time    `json:"captured_at"`
}

type ModelLimit struct {
	Name   string `json:"name"`
	Window Window `json:"window"`
}

func StatePath() string { return filepath.Join(ClaudeDir(), "claudebar.json") }

func ClaudeDir() string { return filepath.Join(gg.Try1(os.UserHomeDir()), ".claude") }

// Claude Code config dirs the app reads: ~/.claude plus one dir per line of
// ~/.config/claudebar/dirs, e.g. the folder a Claude Code container mounts as its
// ~/.claude (`make install CLAUDE_DIR=...` writes it).
func ClaudeDirs() []string {
	out := []string{ClaudeDir()}
	body, err := os.ReadFile(DirsConfigPath())
	if os.IsNotExist(err) {
		return out
	}
	gg.Try(err)
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	return out
}

func DirsConfigPath() string {
	return filepath.Join(gg.Try1(os.UserHomeDir()), ".config", "claudebar", "dirs")
}

// Limits are per account, so the freshest file wins.
func NewestState(dirs []string) State {
	var out State
	for _, dir := range dirs {
		state := ReadState(filepath.Join(dir, "claudebar.json"))
		if state.CapturedAt.After(out.CapturedAt) {
			out = state
		}
	}
	return out
}

func AnyConnected(dirs []string) bool {
	for _, dir := range dirs {
		if Connected(filepath.Join(dir, "settings.json")) {
			return true
		}
	}
	return false
}

// A missing file is an empty state.
func ReadState(path string) State {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{}
	}
	gg.Try(err)
	return gg.JsonDecodeTo[State](body)
}

// Written via a temp file so the menu bar never reads half of it.
func WriteState(path string, val State) {
	gg.Try(os.MkdirAll(filepath.Dir(path), 0o700))
	tmp := path + ".tmp"
	gg.Try(os.WriteFile(tmp, gg.JsonBytes(val), 0o600))
	gg.Try(os.Rename(tmp, path))
}

// New windows replace old ones; a window missing from the new data stays until it resets.
// Claude Code sends no `rate_limits` before the first API response in a session.
func MergeLimits(prev, next Limits, now time.Time) Limits {
	return Limits{
		FiveHour: mergeWindow(prev.FiveHour, next.FiveHour, now),
		SevenDay: mergeWindow(prev.SevenDay, next.SevenDay, now),
	}
}

func mergeWindow(prev, next Window, now time.Time) Window {
	if next.Has() {
		return next
	}
	if prev.Live(now) {
		return prev
	}
	return Window{}
}
