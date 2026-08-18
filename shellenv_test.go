package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseEnvNul(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]string
	}{
		{
			name: "simple pairs",
			in:   "FOO=bar\x00BAZ=qux\x00",
			want: map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name: "value containing newlines",
			in:   "MULTI=line one\nline two\x00NEXT=ok\x00",
			want: map[string]string{"MULTI": "line one\nline two", "NEXT": "ok"},
		},
		{
			name: "value containing equals sign",
			in:   "QUERY=a=b=c\x00",
			want: map[string]string{"QUERY": "a=b=c"},
		},
		{
			name: "entry without equals is skipped",
			in:   "GARBAGE\x00GOOD=yes\x00",
			want: map[string]string{"GOOD": "yes"},
		},
		{
			name: "empty input",
			in:   "",
			want: map[string]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseEnvNul([]byte(tc.in))
			if len(got) != len(tc.want) {
				t.Fatalf("got %d entries (%v), want %d (%v)", len(got), got, len(tc.want), tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("key %q: got %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestHarvestEnvRunsCommandAndCaches(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "env.cache")

	first, err := HarvestEnv([]string{"/bin/sh", "-c", `printf 'FOO=first\0'`}, cache, time.Minute, false)
	if err != nil {
		t.Fatalf("first harvest: %v", err)
	}
	if first["FOO"] != "first" {
		t.Fatalf("first harvest FOO = %q, want %q", first["FOO"], "first")
	}

	// Fresh cache: a different command must NOT run; cached value wins.
	second, err := HarvestEnv([]string{"/bin/sh", "-c", `printf 'FOO=second\0'`}, cache, time.Minute, false)
	if err != nil {
		t.Fatalf("second harvest: %v", err)
	}
	if second["FOO"] != "first" {
		t.Errorf("cached harvest FOO = %q, want cached %q", second["FOO"], "first")
	}

	// Bypass ignores the fresh cache and refreshes it.
	third, err := HarvestEnv([]string{"/bin/sh", "-c", `printf 'FOO=third\0'`}, cache, time.Minute, true)
	if err != nil {
		t.Fatalf("bypass harvest: %v", err)
	}
	if third["FOO"] != "third" {
		t.Errorf("bypass harvest FOO = %q, want %q", third["FOO"], "third")
	}

	// Zero TTL: cache is always stale.
	fourth, err := HarvestEnv([]string{"/bin/sh", "-c", `printf 'FOO=fourth\0'`}, cache, 0, false)
	if err != nil {
		t.Fatalf("expired harvest: %v", err)
	}
	if fourth["FOO"] != "fourth" {
		t.Errorf("expired harvest FOO = %q, want %q", fourth["FOO"], "fourth")
	}
}

func TestHarvestEnvScrubsInheritedEnvironment(t *testing.T) {
	// The plugin inherits the herdr server's stale environment; the harvest
	// shell must start clean (like a fresh terminal) so its startup files
	// rebuild everything from scratch, and must not see stale values.
	t.Setenv("STALE_MARKER", "leaked")
	cache := filepath.Join(t.TempDir(), "env.cache")

	got, err := HarvestEnv([]string{"/bin/sh", "-c", `printf 'SEEN=%s\0HOME_SET=%s\0' "$STALE_MARKER" "$HOME"`}, cache, time.Minute, false)
	if err != nil {
		t.Fatalf("harvest: %v", err)
	}
	if got["SEEN"] != "" {
		t.Errorf("inherited STALE_MARKER leaked into harvest shell: %q", got["SEEN"])
	}
	if got["HOME_SET"] == "" {
		t.Error("HOME was not preserved for the harvest shell")
	}
}

func TestHarvestEnvCommandFailure(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "env.cache")
	_, err := HarvestEnv([]string{"/bin/sh", "-c", "exit 3"}, cache, time.Minute, false)
	if err == nil {
		t.Fatal("expected error from failing command, got nil")
	}
}

// The harvest shell is interactive (`$SHELL -ilc`), and an interactive zsh
// that can open its controlling terminal makes itself the terminal's
// foreground process group. Spawned from a shell hook — whose controlling
// terminal is the user's pane — that stole the tty from the user's own
// foreground command, which then got SIGTTOU on its next tcsetattr ("zsh:
// suspended (tty output)  brew upgrade"). The shell must run in its own
// session, so it has no controlling terminal to grab.
func TestHarvestCommandRunsInOwnSession(t *testing.T) {
	cmd := harvestCommand(context.Background(), []string{"/bin/sh", "-c", "true"})
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatalf("harvest command not detached into its own session: %+v", cmd.SysProcAttr)
	}
	if cmd.Stdin != nil {
		t.Errorf("harvest command inherits stdin: %v", cmd.Stdin)
	}
	// Behavioral check where the test itself has a controlling terminal: the
	// child must not see one.
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no controlling terminal; behavioral check skipped")
	}
	tty.Close()
	env, err := HarvestEnv([]string{"/bin/sh", "-c", `if (: </dev/tty) 2>/dev/null; then printf 'CTTY=yes\0'; else printf 'CTTY=no\0'; fi`},
		filepath.Join(t.TempDir(), "cache"), time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	if env["CTTY"] != "no" {
		t.Errorf("harvest shell can open its controlling terminal (CTTY=%q); it would grab the tty", env["CTTY"])
	}
}
