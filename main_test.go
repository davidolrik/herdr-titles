package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureSnapshotResult loads testdata/snapshot.json (a full CLI-era
// response) and returns its result payload, the shape session.snapshot
// serves over the socket.
func fixtureSnapshotResult(t *testing.T) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile("testdata/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	var full struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		t.Fatal(err)
	}
	// The protocol is line-framed: the fixture's pretty-printed JSON must be
	// compacted to a single line before a fake server may serve it.
	var buf bytes.Buffer
	if err := json.Compact(&buf, full.Result); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestApplyTitleSetsOnlyWhenChanged(t *testing.T) {
	api := newFakeAPI(t)
	statePath := filepath.Join(t.TempDir(), "last_title")

	changed, err := ApplyTitle(api.sockPath, statePath, "hello")
	if err != nil {
		t.Fatalf("ApplyTitle: %v", err)
	}
	if !changed {
		t.Error("first ApplyTitle reported unchanged")
	}

	changed, err = ApplyTitle(api.sockPath, statePath, "hello")
	if err != nil {
		t.Fatalf("second ApplyTitle: %v", err)
	}
	if changed {
		t.Error("second ApplyTitle with same title reported changed")
	}

	titleSets, _, _ := api.recorded()
	if len(titleSets) != 1 || titleSets[0] != "hello" {
		t.Errorf("title sets = %v, want exactly one 'hello'", titleSets)
	}
}

// TestEndToEnd builds the real binary and runs it against a fake API socket,
// asserting the full pipeline: config load, env harvest, snapshot fetch,
// template evaluation, and set-only-when-changed.
func TestEndToEnd(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr-titles")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	api := newFakeAPI(t)
	api.mu.Lock()
	api.snapshot = fixtureSnapshotResult(t)
	api.mu.Unlock()

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
  enabled      = false # keep this test to the window-title path
  watch_titles = false # and keep watchdogs from spawning daemons at the fake
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
			"HERDR_SESSION=testsess",
			"HERDR_SOCKET_PATH="+api.sockPath,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("binary run (%s): %v\n%s", event, err, out)
		}
	}

	run("tab.focused")

	want := "testsess|herdr-overseer|1|W1|live"
	titleSets, _, snapshots := api.recorded()
	if snapshots != 1 || len(titleSets) != 1 || titleSets[0] != want {
		t.Fatalf("after first run: snapshots=%d titleSets=%v, want 1 snapshot and [%s]", snapshots, titleSets, want)
	}

	// Second run: same snapshot, so the title must not be set again.
	run("pane.agent_status_changed")
	titleSets, _, snapshots = api.recorded()
	if snapshots != 2 || len(titleSets) != 1 {
		t.Errorf("after second run: snapshots=%d titleSets=%v, want one extra snapshot only", snapshots, titleSets)
	}

	// State is per-session: several herdr sessions share one state dir, and one
	// session's record must never suppress another session's push.
	if _, err := os.Stat(filepath.Join(stateDir, "last_title.testsess")); err != nil {
		t.Errorf("per-session state file missing: %v", err)
	}
}

// requireShell skips the test when shell is not installed: the emitted
// integration is proven live in zsh/bash/fish where they exist (a dev
// machine, macOS CI); a Linux runner without zsh must not fail on that.
func requireShell(t *testing.T, shell string) {
	t.Helper()
	if _, err := exec.LookPath(shell); err != nil {
		t.Skipf("%s not installed", shell)
	}
}

// init-config writes the documented default config (the init-config action).
// The bare `init` verb belongs to the shell integration (`init <shell>`), so
// with no shell it must fail with usage rather than silently write a config.
func TestInitConfigSubcommand(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr-titles")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	configDir := filepath.Join(dir, "config")

	cmd := exec.Command(bin, "init-config")
	cmd.Env = append(os.Environ(), "HERDR_PLUGIN_CONFIG_DIR="+configDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init-config: %v\n%s", err, out)
	}
	wantPath := filepath.Join(configDir, "config.hcl")
	if !strings.Contains(string(out), wantPath) {
		t.Errorf("init-config output %q does not mention %q", out, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("config.hcl not created: %v", err)
	}

	// Second invocation refuses to overwrite and exits non-zero.
	cmd = exec.Command(bin, "init-config")
	cmd.Env = append(os.Environ(), "HERDR_PLUGIN_CONFIG_DIR="+configDir)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("second init-config succeeded, want refusal; output: %s", out)
	}

	// Bare `init` is the shell integration and needs a shell: usage error,
	// no config written.
	otherDir := filepath.Join(dir, "other")
	cmd = exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HERDR_PLUGIN_CONFIG_DIR="+otherDir)
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("bare init succeeded, want usage error; output: %s", out)
	}
	if !strings.Contains(string(out), "usage") && !strings.Contains(string(out), "init <shell>") {
		t.Errorf("bare init error %q does not show usage", out)
	}
	if _, err := os.Stat(filepath.Join(otherDir, "config.hcl")); err == nil {
		t.Errorf("bare init wrote a config file")
	}
}

// `init <shell>` prints the shell integration for eval: the hook script with
// THIS binary's absolute path baked in, so the emitted script needs none of
// the sourced-file self-location the on-disk hooks use (which breaks under
// eval, where there is no sourced file). Each script must parse under its
// real shell.
func TestInitShellEmitsHookWithBinaryPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr-titles")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	shells := map[string]struct {
		check   []string // syntax-check invocation, script on stdin
		selfLoc string   // sourced-file self-location that must NOT be emitted
	}{
		"zsh":  {[]string{"zsh", "-n"}, "${(%):-%N}"},
		"bash": {[]string{"bash", "-n"}, "BASH_SOURCE"},
		"fish": {[]string{"fish", "--no-execute"}, "status current-filename"},
	}
	for shell, want := range shells {
		t.Run(shell, func(t *testing.T) {
			out, err := exec.Command(bin, "init", shell).Output()
			if err != nil {
				t.Fatalf("init %s: %v", shell, err)
			}
			script := string(out)
			if !strings.Contains(script, bin) {
				t.Errorf("init %s output does not bake in the binary path %q", shell, bin)
			}
			if strings.Contains(script, want.selfLoc) {
				t.Errorf("init %s output still self-locates via %q", shell, want.selfLoc)
			}
			if _, err := exec.LookPath(want.check[0]); err != nil {
				t.Skipf("%s not installed", want.check[0])
			}
			chk := exec.Command(want.check[0], want.check[1:]...)
			chk.Stdin = strings.NewReader(script)
			if res, err := chk.CombinedOutput(); err != nil {
				t.Fatalf("init %s output does not parse under %s: %v\n%s", shell, want.check[0], err, res)
			}
		})
	}

	// Unknown shell: usage error, nothing on stdout.
	out, err := exec.Command(bin, "init", "tcsh").CombinedOutput()
	if err == nil {
		t.Fatalf("init tcsh succeeded; output: %s", out)
	}
	if !strings.Contains(string(out), "zsh") {
		t.Errorf("init tcsh error %q does not list supported shells", out)
	}
}

// The emitted zsh/bash integration also publishes a pane title every prompt
// (OSC 2, from the cwd basename by default), because those shells set no
// terminal title on their own and terminal_titles has nothing to read
// otherwise. It is user-overridable: define _herdr_titles_title yourself, or
// set HERDR_TITLES_NO_TITLE=1 to keep your shell's own title. fish already
// sets a title through fish_title, so its integration only documents that.
func TestInitShellEmitsTitleSetter(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr-titles")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	for _, shell := range []string{"zsh", "bash"} {
		out, err := exec.Command(bin, "init", shell).Output()
		if err != nil {
			t.Fatalf("init %s: %v", shell, err)
		}
		script := string(out)
		for _, want := range []string{"_herdr_titles_title", "HERDR_TITLES_NO_TITLE", `\e]2;`} {
			if !strings.Contains(script, want) {
				t.Errorf("init %s output lacks %q", shell, want)
			}
		}
	}
	out, err := exec.Command(bin, "init", "fish").Output()
	if err != nil {
		t.Fatalf("init fish: %v", err)
	}
	if strings.Contains(string(out), "_herdr_titles_title") {
		t.Errorf("init fish emits a title setter; fish_title already owns the title")
	}
	if !strings.Contains(string(out), "fish_title") {
		t.Errorf("init fish output does not point at fish_title")
	}

	requireShell(t, "zsh")
	// zsh: the default title is the cwd basename; a user-defined
	// _herdr_titles_title wins; the opt-out defines nothing. An icons-off
	// config keeps this hermetic (with icons on, `init` bakes the shell's
	// glyph into the default title — covered by its own test).
	plainCfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(plainCfg, "config.hcl"), []byte("template = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	zsh := func(env, body string) string {
		t.Helper()
		cmd := exec.Command("zsh", "-c", env+` eval "$(`+bin+` init zsh)"; `+body)
		cmd.Env = append(os.Environ(), "HERDR_PLUGIN_CONFIG_DIR="+plainCfg)
		res, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("zsh: %v\n%s", err, res)
		}
		return strings.TrimSpace(string(res))
	}
	if got := zsh(`HERDR_PANE_ID=x HERDR_TAB_ID=x`, `cd /tmp && print -r -- "$(_herdr_titles_title)"`); got != "tmp" {
		t.Errorf("default title = %q, want cwd basename tmp", got)
	}
	if got := zsh(`HERDR_PANE_ID=x HERDR_TAB_ID=x`, `_herdr_titles_title() { print mine }; eval "$(`+bin+` init zsh)"; print -r -- "$(_herdr_titles_title)"`); got != "mine" {
		t.Errorf("user override lost = %q, want mine", got)
	}
	if got := zsh(`HERDR_PANE_ID=x HERDR_TAB_ID=x HERDR_TITLES_NO_TITLE=1`, `print -r -- "${+functions[_herdr_titles_precmd]}"`); got != "0" {
		t.Errorf("opt-out still defined the title precmd: %q", got)
	}
}

// The prompt title the zsh/bash integration publishes carries the SHELL's
// icon, so a plain-shell tab named after its cwd looks like every other tab
// (program tabs get their program's icon from the plugin). The shell can't
// read the HCL config, so `init` bakes the resolved glyph in at emit time —
// honoring icons.enabled, style, and a custom icons.map entry — and bakes in
// nothing when icons are off.
func TestInitShellBakesShellIconIntoPromptTitle(t *testing.T) {
	requireShell(t, "zsh")
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr-titles")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	sink := filepath.Join(dir, "tty.out")
	promptTitle := func(config string) string {
		t.Helper()
		configDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(configDir, "config.hcl"), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sink, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("zsh", "-c", `eval "$(`+bin+` init zsh)"; cd /tmp; _herdr_titles_precmd; sleep 0.05`)
		cmd.Env = append(os.Environ(), "HERDR_PANE_ID=x", "HERDR_TAB_ID=x", "HERDR_TITLES_TTY="+sink,
			"HERDR_PLUGIN_CONFIG_DIR="+configDir, "HERDR_SOCKET_PATH=/nonexistent/herdr.sock")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("zsh: %v\n%s", err, out)
		}
		got, _ := os.ReadFile(sink)
		return string(got)
	}
	// Icons on (default style name_and_icon): the builtin zsh glyph + cwd.
	if got := promptTitle("template = \"x\"\ntabs {\n  icons {\n    enabled = true\n  }\n}\n"); got != "\x1b]2;\ue795 tmp\a" {
		t.Errorf("icons on: prompt title = %q, want zsh glyph + tmp", got)
	}
	// A custom map entry for the shell wins.
	if got := promptTitle("template = \"x\"\ntabs {\n  icons {\n    enabled = true\n    map = { zsh = \"Z\" }\n  }\n}\n"); got != "\x1b]2;Z tmp\a" {
		t.Errorf("custom map: prompt title = %q, want Z tmp", got)
	}
	// Icons off: bare cwd.
	if got := promptTitle("template = \"x\"\n"); got != "\x1b]2;tmp\a" {
		t.Errorf("icons off: prompt title = %q, want bare tmp", got)
	}
	// style = "name": no glyph even with icons on.
	if got := promptTitle("template = \"x\"\ntabs {\n  icons {\n    enabled = true\n    style = \"name\"\n  }\n}\n"); got != "\x1b]2;tmp\a" {
		t.Errorf("style=name: prompt title = %q, want bare tmp", got)
	}
}

// Under terminal_titles the program a command starts reaches the tab through
// the pane TITLE (the daemon is the single writer): the zsh integration's
// preexec publishes the resolved program name as an OSC 2 title for a real
// program, and publishes nothing for a builtin/function (cd, z, aliases) so
// the shell's cwd title stands — no shell-name flash. A program that sets its
// own title (nvim) simply overrides it moments later.
func TestInitZshPreexecPublishesProgramTitle(t *testing.T) {
	requireShell(t, "zsh")
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr-titles")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	// Capture what preexec writes to the terminal by pointing the title
	// sink at a file: the hook writes titles to $HERDR_TITLES_TTY when set
	// (a test seam; it defaults to /dev/tty).
	sink := filepath.Join(dir, "tty.out")
	run := func(cmdline string) string {
		t.Helper()
		if err := os.WriteFile(sink, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("zsh", "-c",
			`eval "$(`+bin+` init zsh)"; _hwt_preexec `+shellQuote(cmdline)+` `+shellQuote(cmdline)+`; sleep 0.1`)
		cmd.Env = append(os.Environ(), "HERDR_PANE_ID=x", "HERDR_TAB_ID=x", "HERDR_TITLES_TTY="+sink,
			"HERDR_SOCKET_PATH=/nonexistent/herdr.sock")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("zsh: %v\n%s", err, out)
		}
		got, _ := os.ReadFile(sink)
		return string(got)
	}
	// `env` exists everywhere (unlike an editor), so the test has no hidden
	// dependency on what is installed on the machine.
	if got := run("env FOO=1 true"); got != "\x1b]2;env\a" {
		t.Errorf("real program: title write = %q, want OSC 2 env", got)
	}
	if got := run("/usr/bin/env"); got != "\x1b]2;env\a" {
		t.Errorf("absolute path: title write = %q, want basename env", got)
	}
	if got := run("cd /tmp"); got != "" {
		t.Errorf("builtin cd: title write = %q, want nothing (cwd title stands)", got)
	}
	// Opt-out disables the program title too, not just the prompt title.
	cmd := exec.Command("zsh", "-c", `eval "$(`+bin+` init zsh)"; _hwt_preexec 'env' 'env'; sleep 0.1`)
	if err := os.WriteFile(sink, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(), "HERDR_PANE_ID=x", "HERDR_TAB_ID=x", "HERDR_TITLES_TTY="+sink,
		"HERDR_TITLES_NO_TITLE=1", "HERDR_SOCKET_PATH=/nonexistent/herdr.sock")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("zsh: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(sink); len(got) != 0 {
		t.Errorf("HERDR_TITLES_NO_TITLE=1 still published a program title: %q", got)
	}
}

func TestFastPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr-titles")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	api := newFakeAPI(t)
	api.setTab("w1:t1", "")
	stateDir := filepath.Join(dir, "state")
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// watch_titles off so the fast path's daemon probe stays inert.
	if err := os.WriteFile(filepath.Join(configDir, "config.hcl"),
		[]byte("template = \"x\"\ntabs { watch_titles = false }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(),
			"HERDR_PLUGIN_CONFIG_DIR="+configDir,
			"HERDR_PLUGIN_STATE_DIR="+stateDir,
			"HERDR_SESSION=fastsess",
			"HERDR_TAB_ID=w1:t1",
			"HERDR_PANE_ID=w1:p1",
			"HERDR_SOCKET_PATH="+api.sockPath,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("binary %v: %v\n%s", args, err, out)
		}
	}

	// preexec with a resolvable program: renamed from the typed command line.
	run("preexec", "nvim main.go")
	_, renames, _ := api.recorded()
	if len(renames) != 1 || renames[0] != "w1:t1=nvim" {
		t.Fatalf("preexec renames = %v, want [w1:t1=nvim]", renames)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "tabstate.fastsess.json")); err != nil {
		t.Fatalf("per-session tab state missing: %v", err)
	}

	// sampled preexec: the pane's real process wins over the typed word.
	api.setProcessInfo("w1:p1", "htop", "htop")
	run("preexec", "some-function", "shell")
	_, renames, _ = api.recorded()
	if len(renames) != 2 || renames[1] != "w1:t1=htop" {
		t.Fatalf("sampled renames = %v, want trailing w1:t1=htop", renames)
	}

	// precmd: back to the shell name passed by the hook.
	run("precmd", "fish")
	_, renames, _ = api.recorded()
	if len(renames) != 3 || renames[2] != "w1:t1=fish" {
		t.Fatalf("precmd renames = %v, want trailing w1:t1=fish", renames)
	}

	// tabs disabled: the fast path must not touch the API at all.
	if err := os.WriteFile(filepath.Join(configDir, "config.hcl"),
		[]byte("template = \"x\"\ntabs { enabled = false }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := len(renames)
	run("preexec", "nvim x")
	_, renames, _ = api.recorded()
	if len(renames) != before {
		t.Fatalf("disabled tabs still renamed: %v", renames)
	}

	// terminal titles with the daemon (watch_titles defaults true): the hook
	// reconciles ITS OWN tab from a fresh snapshot, computing exactly what a
	// full pass would — so it can never disagree with the daemon (the single
	// other writer), yet a pane that publishes no title (a shell without the
	// integration, HERDR_TITLES_NO_TITLE=1) still follows its foreground
	// program at preexec and is restored at precmd without waiting for some
	// unrelated event's full pass. A titled pane is named from its title,
	// never from process-info. The hook still probes for a dead daemon;
	// daemonAlive's O_CREATE open leaves the lock file behind, so its
	// reappearance proves the probe ran.
	if err := os.WriteFile(filepath.Join(configDir, "config.hcl"),
		[]byte("template = \"x\"\ntabs { terminal_titles = true }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(stateDir, "watch.lock.fastsess")
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("lock file missing before terminal-titles run: %v", err)
	}
	// The tab is owned from the earlier renames (last set to "fish"); the
	// snapshot is what the hook reads now.
	api.setTab("w1:t1", "fish")
	untitled := singlePaneSnap("fish")
	api.setSnapshot(t, untitled)
	api.setProcessInfo("w1:p1", "hx", "hx x")
	run("preexec", "hx x")
	_, renames, _ = api.recorded()
	if len(renames) != before+1 || renames[before] != "w1:t1=hx" {
		t.Fatalf("terminal_titles=true, untitled pane preexec: renames = %v, want trailing w1:t1=hx", renames)
	}
	untitled.Tabs[0].Label = "hx"
	api.setSnapshot(t, untitled)
	api.setProcessInfo("w1:p1", "zsh", "-zsh")
	run("precmd", "zsh")
	_, renames, _ = api.recorded()
	if len(renames) != before+2 || renames[before+1] != "w1:t1=zsh" {
		t.Fatalf("terminal_titles=true, untitled pane precmd: renames = %v, want trailing w1:t1=zsh", renames)
	}
	titled := singlePaneSnap("zsh")
	titled.Panes[0].Title = "user@host: ~/proj"
	api.setSnapshot(t, titled)
	api.setProcessInfo("w1:p1", "hx", "hx x")
	run("preexec", "hx x")
	run("precmd", "zsh")
	_, renames, _ = api.recorded()
	if len(renames) != before+3 || renames[before+2] != "w1:t1=user@host: ~/proj" {
		t.Fatalf("terminal_titles=true, titled pane: renames = %v, want one trailing rename to the title", renames)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("terminal-titles fast path skipped the daemon probe: %v", err)
	}
}
