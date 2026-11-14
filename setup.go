package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitranim/gg"
)

// Connecting to Claude Code: `statusLine` in ~/.claude/settings.json runs
// `claudebar statusline`. The previous status line command is kept via `-then`.

func SettingsPath() string {
	return filepath.Join(gg.Try1(os.UserHomeDir()), ".claude", "settings.json")
}

func Connected(path string) bool {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false
	}
	gg.Try(err)
	return strings.Contains(statusLineCommand(body), "claudebar")
}

// Before writing: a copy to settings.json.claudebar-backup.
func Connect(path string, exe string) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		body = []byte(`{}`)
	} else {
		gg.Try(err)
		gg.Try(os.WriteFile(path+".claudebar-backup", body, 0o600))
	}
	gg.Try(os.MkdirAll(filepath.Dir(path), 0o700))
	gg.Try(os.WriteFile(path, ConnectSettings(body, exe), 0o600))
}

func ConnectSettings(body []byte, exe string) []byte {
	settings := map[string]any{}
	gg.JsonDecode(body, &settings)

	// Already connected: the command stays as is, with its `-then`.
	cmd := statusLineCommand(body)
	if !strings.Contains(cmd, "claudebar") {
		prev := cmd
		cmd = shellQuote(exe) + " statusline"
		if prev != "" {
			cmd += " -then " + shellQuote(prev)
		}
	}

	line, _ := settings["statusLine"].(map[string]any)
	if line == nil {
		line = map[string]any{}
	}
	line["type"] = "command"
	line["command"] = cmd
	settings["statusLine"] = line

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	gg.Try(enc.Encode(settings))
	return buf.Bytes()
}

func statusLineCommand(body []byte) string {
	var settings struct {
		StatusLine struct {
			Command string `json:"command"`
		} `json:"statusLine"`
	}
	gg.JsonDecode(body, &settings)
	return settings.StatusLine.Command
}

func shellQuote(val string) string {
	return "'" + strings.ReplaceAll(val, "'", `'\''`) + "'"
}

func Executable() string {
	return gg.Try1(filepath.EvalSymlinks(gg.Try1(os.Executable())))
}
