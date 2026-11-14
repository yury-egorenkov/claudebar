package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"time"

	"github.com/mitranim/gg"
)

// Second data source: the usage endpoint Claude Code itself calls.
// The OAuth token of Claude Code goes only to api.anthropic.com. It is read, never
// refreshed: refreshing rotates the refresh token and would log Claude Code out.
// Claude Code refreshes it on its own; an expired token waits for that.

const (
	UsageURL = "https://api.anthropic.com/api/oauth/usage"
	// How often to ask the API. Anthropic answers 429 when polled harder.
	UsageEvery = 2 * time.Minute
	// A fetch older than this is considered dead and may be retried.
	fetchLockTTL = 30 * time.Second
)

func ConfigDir() string { return filepath.Join(gg.Try1(os.UserHomeDir()), ".config", "claudebar") }

// Last API result, in the same format as the status line file.
func UsagePath() string { return filepath.Join(ConfigDir(), "usage.json") }

// Last fetch error, shown in the app; empty after a successful fetch.
func UsageErrPath() string { return filepath.Join(ConfigDir(), "usage-error.txt") }

func fetchLockPath() string { return filepath.Join(ConfigDir(), "fetch.lock") }

// Starts `claudebar-cli fetch` in the background when the API result is stale,
// so `json` returns at once and the app never waits for the network.
func FetchIfStale(now time.Time) {
	if now.Sub(ReadState(UsagePath()).CapturedAt) < UsageEvery || now.Sub(fileTime(UsageErrPath())) < UsageEvery {
		return
	}
	if now.Sub(fileTime(fetchLockPath())) < fetchLockTTL {
		return
	}
	gg.Try(os.MkdirAll(ConfigDir(), 0o700))
	gg.Try(os.WriteFile(fetchLockPath(), nil, 0o600))

	proc := exec.Command(Executable(), "fetch")
	// Own session: the fetch outlives this process and gets no signals meant for it.
	proc.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	gg.Try(proc.Start())
	gg.Try(proc.Process.Release())
}

// Zero time when the file is missing.
func fileTime(path string) time.Time {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return time.Time{}
	}
	gg.Try(err)
	return info.ModTime()
}

// `claudebar-cli fetch`: one API request; the result or the error goes to a file.
func RunFetch() {
	defer os.Remove(fetchLockPath())

	err := gg.Catch(func() {
		now := time.Now()
		state := FetchUsage(ReadToken(ClaudeDirs(), now))
		state.CapturedAt = now
		WriteState(UsagePath(), state)
		if week := state.Limits.SevenDay; week.Has() {
			AppendHistory(HistoryPath(), Sample{At: now.Unix(), Used: week.Used, ResetsAt: week.ResetsAt}, now)
		}
	})
	if err != nil {
		gg.Try(os.WriteFile(UsageErrPath(), []byte(err.Error()), 0o600))
		return
	}
	err = os.Remove(UsageErrPath())
	if !os.IsNotExist(err) {
		gg.Try(err)
	}
}

func ReadUsageErr() string {
	body, err := os.ReadFile(UsageErrPath())
	if os.IsNotExist(err) {
		return ""
	}
	gg.Try(err)
	return string(body)
}

type Credentials struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   int64  `json:"expiresAt"` // epoch milliseconds
	} `json:"claudeAiOauth"`
}

func (self Credentials) Live(now time.Time) bool {
	return self.ClaudeAiOauth.AccessToken != "" && now.Before(time.UnixMilli(self.ClaudeAiOauth.ExpiresAt))
}

// Token with the latest expiry among `.credentials.json` in the Claude dirs
// (Claude Code on Linux, e.g. in a container) and the macOS Keychain (Claude Code on the Mac).
func ReadToken(dirs []string, now time.Time) string {
	var all []Credentials
	for _, dir := range dirs {
		body, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
		if os.IsNotExist(err) {
			continue
		}
		gg.Try(err)
		all = append(all, gg.JsonDecodeTo[Credentials](body))
	}
	// The Keychain only when the files have no live token: after every rebuild macOS
	// asks again whether this helper may read it.
	if runtime.GOOS == "darwin" && !slices.ContainsFunc(all, func(cred Credentials) bool { return cred.Live(now) }) {
		if body := keychainCredentials(); body != nil {
			all = append(all, gg.JsonDecodeTo[Credentials](body))
		}
	}
	return BestToken(all, now)
}

func BestToken(all []Credentials, now time.Time) string {
	var best Credentials
	for _, cred := range all {
		if cred.ClaudeAiOauth.ExpiresAt > best.ClaudeAiOauth.ExpiresAt {
			best = cred
		}
	}
	if best.ClaudeAiOauth.AccessToken == "" {
		panic(gg.Errf("no Claude Code login found: no .credentials.json in %v and nothing in the Keychain", ClaudeDirs()))
	}
	if !best.Live(now) {
		panic(gg.Errf("Claude Code token expired at %v; it renews when Claude Code runs", time.UnixMilli(best.ClaudeAiOauth.ExpiresAt).Format("Jan 2 15:04")))
	}
	return best.ClaudeAiOauth.AccessToken
}

// nil when Claude Code never logged in on this Mac (the item is missing).
func keychainCredentials() []byte {
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 { // errSecItemNotFound
			return nil
		}
		panic(gg.Wrapf(err, "reading Claude Code credentials from the Keychain"))
	}
	return out
}

func FetchUsage(token string) State {
	req := gg.Try1(http.NewRequest(http.MethodGet, UsageURL, nil))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "claudebar/1.0")

	client := http.Client{Timeout: 15 * time.Second}
	res := gg.Try1(client.Do(req))
	defer res.Body.Close()
	body := gg.Try1(io.ReadAll(res.Body))

	if res.StatusCode != http.StatusOK {
		panic(gg.Errf("usage API: HTTP %d: %.200s", res.StatusCode, body))
	}
	return ParseUsage(body)
}

type usageWindow struct {
	Utilization float64   `json:"utilization"` // % used, 0–100
	ResetsAt    time.Time `json:"resets_at"`
}

// Absent windows come as `null`, hence the pointers.
func ParseUsage(body []byte) State {
	var res struct {
		FiveHour *usageWindow `json:"five_hour"`
		SevenDay *usageWindow `json:"seven_day"`
		Limits   []struct {
			Kind     string    `json:"kind"`
			Percent  float64   `json:"percent"`
			ResetsAt time.Time `json:"resets_at"`
			Scope    struct {
				Model struct {
					DisplayName string `json:"display_name"`
				} `json:"model"`
			} `json:"scope"`
		} `json:"limits"`
	}
	gg.Try(json.Unmarshal(body, &res))

	out := State{Limits: Limits{FiveHour: res.FiveHour.window(), SevenDay: res.SevenDay.window()}}
	for _, lim := range res.Limits {
		name := lim.Scope.Model.DisplayName
		if lim.Kind == "weekly_scoped" && name != "" {
			out.Models = append(out.Models, ModelLimit{Name: name, Window: Window{Used: lim.Percent, ResetsAt: lim.ResetsAt.Unix()}})
		}
	}
	return out
}

func (self *usageWindow) window() Window {
	if self == nil {
		return Window{}
	}
	return Window{Used: self.Utilization, ResetsAt: self.ResetsAt.Unix()}
}

func (self Credentials) String() string {
	return fmt.Sprintf("Credentials{expires %v}", time.UnixMilli(self.ClaudeAiOauth.ExpiresAt))
}
