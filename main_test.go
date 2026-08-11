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
	bin := filepath.Join(dir, "herdr-titles")
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
template = "${session}|${workspace}|${tab}|${attention}|${getenv("E2E_MARKER")}"

env {
  command = ["/bin/sh", "-c", "printf 'E2E_MARKER=live\\0'"]
  ttl     = "1h"
}

attention {
  statuses = ["working"]
  icons    = { working = "W" }
}

tabs {
  enabled = false # keep this test's herdr call log to the window-title path
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
			"HERDR_SOCKET_PATH=/nonexistent/herdr.sock", // keep spawned daemons stillborn
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
	bin := filepath.Join(dir, "herdr-titles")
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

// fakeFastHerdr serves `tab get` with a fixed label, `pane process-info` from
// infoDir, and records everything.
func fakeFastHerdr(t *testing.T, dir, callsPath, infoDir, label string) string {
	t.Helper()
	// The label goes through a file, not through the script text: Go's %q
	// escapes private-use glyphs into \U literals a shell won't decode.
	labelPath := filepath.Join(dir, "label.txt")
	if err := os.WriteFile(labelPath, []byte(label), 0o644); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
if [ "$1" = "tab" ] && [ "$2" = "get" ]; then
  printf '{"result":{"tab":{"tab_id":"%%s","label":"%%s"}}}' "$3" "$(cat %q)"
fi
if [ "$1" = "pane" ] && [ "$2" = "process-info" ]; then
  pane=$(printf '%%s' "$4" | tr ':' '_')
  cat %q/"$pane".json 2>/dev/null || exit 1
fi
`, callsPath, labelPath, infoDir)
	path := filepath.Join(dir, "herdr")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFastPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr-titles")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	calls := filepath.Join(dir, "calls.log")
	infoDir := filepath.Join(dir, "info")
	stateDir := filepath.Join(dir, "state")
	configDir := filepath.Join(dir, "config")

	run := func(herdrBin string, args ...string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(),
			"HERDR_PLUGIN_CONFIG_DIR="+configDir,
			"HERDR_PLUGIN_STATE_DIR="+stateDir,
			"HERDR_BIN_PATH="+herdrBin,
			"HERDR_SESSION=fastsess",
			"HERDR_TAB_ID=w1:t1",
			"HERDR_PANE_ID=w1:p1",
			"HERDR_SOCKET_PATH=/nonexistent/herdr.sock", // keep spawned daemons stillborn
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("binary %v: %v\n%s", args, err, out)
		}
	}
	tabRenames := func(path string) []string {
		var out []string
		for _, line := range readCalls(t, path) {
			if strings.HasPrefix(line, "tab rename ") {
				out = append(out, strings.TrimPrefix(line, "tab rename "))
			}
		}
		return out
	}

	// preexec with a resolvable program: renamed from the typed command line.
	herdrBin := fakeFastHerdr(t, dir, calls, infoDir, "")
	run(herdrBin, "preexec", "nvim main.go")
	if got := tabRenames(calls); len(got) != 1 || got[0] != "w1:t1 nvim" {
		t.Fatalf("preexec renames = %v, want [w1:t1 nvim]", got)
	}
	stateFile := filepath.Join(stateDir, "tabstate.fastsess.json")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("per-session tab state missing: %v", err)
	}

	// sampled preexec: the pane's real process wins over the typed word.
	calls2 := filepath.Join(dir, "calls2.log")
	herdrBin = fakeFastHerdr(t, dir, calls2, infoDir, "nvim")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	info := `{"result":{"process_info":{"foreground_process_group_id":7,
	  "foreground_processes":[{"pid":7,"argv0":"htop","cmdline":"htop"}]}}}`
	if err := os.WriteFile(filepath.Join(infoDir, "w1_p1.json"), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	run(herdrBin, "preexec", "some-function", "shell")
	if got := tabRenames(calls2); len(got) != 1 || got[0] != "w1:t1 htop" {
		t.Fatalf("sampled renames = %v, want [w1:t1 htop]", got)
	}

	// precmd: back to the shell name passed by the hook.
	calls3 := filepath.Join(dir, "calls3.log")
	herdrBin = fakeFastHerdr(t, dir, calls3, infoDir, "htop")
	run(herdrBin, "precmd", "fish")
	if got := tabRenames(calls3); len(got) != 1 || got[0] != "w1:t1 fish" {
		t.Fatalf("precmd renames = %v, want [w1:t1 fish]", got)
	}

	// tabs disabled: the fast path must not touch herdr at all.
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	off := "template = \"x\"\ntabs { enabled = false }\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.hcl"), []byte(off), 0o644); err != nil {
		t.Fatal(err)
	}
	calls4 := filepath.Join(dir, "calls4.log")
	herdrBin = fakeFastHerdr(t, dir, calls4, infoDir, "")
	run(herdrBin, "preexec", "nvim x")
	if got := readCalls(t, calls4); len(got) != 0 {
		t.Fatalf("disabled tabs still called herdr: %v", got)
	}
}
