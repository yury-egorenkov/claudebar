package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitranim/gg/gtest"
)

const testExe = "/Applications/ClaudeBar.app/Contents/MacOS/claudebar"

func TestConnectSettings_empty(t *testing.T) {
	defer gtest.Catch(t)

	gtest.Eq(string(ConnectSettings([]byte(`{}`), testExe)), `{
  "statusLine": {
    "command": "'/Applications/ClaudeBar.app/Contents/MacOS/claudebar' statusline",
    "type": "command"
  }
}
`)
}

func TestConnectSettings_prev(t *testing.T) {
	defer gtest.Catch(t)

	// The previous command is kept, other settings stay.
	prev := []byte(`{"model":"opus","statusLine":{"type":"command","command":"echo 'hi'","padding":1}}`)
	out := ConnectSettings(prev, testExe)
	gtest.Eq(statusLineCommand(out), `'`+testExe+`' statusline -then 'echo '\''hi'\'''`)
	gtest.True(strings.Contains(string(out), `"model": "opus"`))
	gtest.True(strings.Contains(string(out), `"padding": 1`))

	// Reconnecting doesn't nest claudebar inside claudebar.
	gtest.Eq(statusLineCommand(ConnectSettings(out, testExe)), statusLineCommand(out))
}

func TestConnect_files(t *testing.T) {
	defer gtest.Catch(t)

	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	gtest.False(Connected(path))
	Connect(path, testExe)
	gtest.True(Connected(path))

	// Existing file: a backup before writing.
	Connect(path, testExe)
	gtest.True(Connected(path + ".claudebar-backup"))
}

func TestParseUsage(t *testing.T) {
	defer gtest.Catch(t)

	reset := time.Date(2026, 11, 21, 13, 0, 0, 0, time.UTC).Unix()
	gtest.Equal(
		ParseUsage([]byte(`{"five_hour":null,"seven_day":{"utilization":2.0,"resets_at":"2026-11-21T13:00:00.243654+00:00","limit_dollars":null},
			"limits":[{"kind":"weekly_all","percent":2,"resets_at":"2026-11-21T13:00:00+00:00","scope":null},
			{"kind":"weekly_scoped","percent":7,"resets_at":"2026-11-21T13:00:00+00:00","scope":{"model":{"id":null,"display_name":"Fable"},"surface":null}}]}`)),
		State{
			Limits: Limits{SevenDay: Window{Used: 2, ResetsAt: reset}},
			Models: []ModelLimit{{Name: "Fable", Window: Window{Used: 7, ResetsAt: reset}}},
		},
	)
}

func TestBestToken(t *testing.T) {
	defer gtest.Catch(t)

	now := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	cred := func(token string, expires time.Time) Credentials {
		var out Credentials
		out.ClaudeAiOauth.AccessToken = token
		out.ClaudeAiOauth.ExpiresAt = expires.UnixMilli()
		return out
	}

	gtest.Eq(BestToken([]Credentials{cred("old", now.Add(time.Hour)), cred("new", now.Add(5*time.Hour))}, now), "new")
	gtest.PanicStr("token expired", func() { BestToken([]Credentials{cred("x", now.Add(-time.Minute))}, now) })
	gtest.PanicStr("no Claude Code login", func() { BestToken(nil, now) })

	// The token never shows up in formatted errors.
	gtest.False(strings.Contains(fmt.Sprint(cred("secret", now)), "secret"))
}
