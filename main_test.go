package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHerdr writes a shell script that logs every invocation to callsPath and
// serves the snapshot fixture for `api snapshot`.
func fakeHerdr(t *testing.T, dir, callsPath string) string {
	t.Helper()
	fixture, err := filepath.Abs("testdata/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
if [ "$1" = "api" ] && [ "$2" = "snapshot" ]; then
  cat %q
fi
`, callsPath, fixture)
	path := filepath.Join(dir, "herdr")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readCalls(t *testing.T, callsPath string) []string {
	t.Helper()
	data, err := os.ReadFile(callsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestApplyTitleSetsOnlyWhenChanged(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls.log")
	bin := fakeHerdr(t, dir, calls)
	statePath := filepath.Join(dir, "last_title")

	changed, err := ApplyTitle(bin, statePath, "hello")
	if err != nil {
		t.Fatalf("ApplyTitle: %v", err)
	}
	if !changed {
		t.Error("first ApplyTitle reported unchanged")
	}

	changed, err = ApplyTitle(bin, statePath, "hello")
	if err != nil {
		t.Fatalf("second ApplyTitle: %v", err)
	}
	if changed {
		t.Error("second ApplyTitle with same title reported changed")
	}

	got := readCalls(t, calls)
	if len(got) != 1 || got[0] != "terminal title set hello" {
		t.Errorf("herdr calls = %v, want exactly one title set", got)
	}
}

// TestEndToEnd builds the real binary and runs it against a fake herdr,
// asserting the full pipeline: config load, env harvest, snapshot fetch,
// template evaluation, and set-only-when-changed.
func TestEndToEnd(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr-window-title")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	calls := filepath.Join(dir, "calls.log")
	herdrBin := fakeHerdr(t, dir, calls)

	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	config := `
template = "${session}|${space}|${tab}|${attention}|${getenv("E2E_MARKER")}"

env {
  command = ["/bin/sh", "-c", "printf 'E2E_MARKER=live\\0'"]
  ttl     = "1h"
}

attention {
  statuses = ["working"]
  icons    = { working = "W" }
}
`
	if err := os.WriteFile(filepath.Join(configDir, "config.hcl"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(event string) {
		t.Helper()
		cmd := exec.Command(bin, event)
		cmd.Env = append(os.Environ(),
			"HERDR_PLUGIN_CONFIG_DIR="+configDir,
			"HERDR_PLUGIN_STATE_DIR="+stateDir,
			"HERDR_BIN_PATH="+herdrBin,
			"HERDR_SESSION=testsess",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("binary run (%s): %v\n%s", event, err, out)
		}
	}

	run("tab.focused")

	want := "terminal title set testsess|herdr-overseer|1|W1|live"
	got := readCalls(t, calls)
	if len(got) != 2 || got[0] != "api snapshot" || got[1] != want {
		t.Fatalf("herdr calls after first run = %v, want [api snapshot, %s]", got, want)
	}

	// Second run: same snapshot, so the title must not be set again.
	run("pane.agent_status_changed")
	got = readCalls(t, calls)
	if len(got) != 3 || got[2] != "api snapshot" {
		t.Errorf("herdr calls after second run = %v, want one extra api snapshot only", got)
	}

	// State is per-session: several herdr sessions share one state dir, and one
	// session's record must never suppress another session's push.
	if _, err := os.Stat(filepath.Join(stateDir, "last_title.testsess")); err != nil {
		t.Errorf("per-session state file missing: %v", err)
	}
}

func TestInitSubcommand(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr-window-title")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	configDir := filepath.Join(dir, "config")

	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HERDR_PLUGIN_CONFIG_DIR="+configDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	wantPath := filepath.Join(configDir, "config.hcl")
	if !strings.Contains(string(out), wantPath) {
		t.Errorf("init output %q does not mention %q", out, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("config.hcl not created: %v", err)
	}

	// Second invocation refuses to overwrite and exits non-zero.
	cmd = exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HERDR_PLUGIN_CONFIG_DIR="+configDir)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("second init succeeded, want refusal; output: %s", out)
	}
}
