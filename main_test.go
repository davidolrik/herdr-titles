package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// In-process coverage of the daemonless terminal-titles hook path: the
// locked read renames from the pane's current title, and a pane that left
// the invoking tab does not cause a rename. The fake API has no event stream,
// so the hook degrades to the single read — the linger is covered separately.
func TestRunFastDaemonlessInProcess(t *testing.T) {
	api := newFakeAPI(t)
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.hcl"),
		[]byte("template = \"x\"\ntabs {\n  terminal_titles = true\n  watch_titles = false\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", configDir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	t.Setenv("HERDR_SESSION", "insess")
	t.Setenv("HERDR_TAB_ID", "w1:t1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_SOCKET_PATH", api.sockPath)
	api.setTab("w1:t1", "1")
	api.setPaneTitle("w1:p1", "w1:t1", "", "make -j all")

	if err := runFast("precmd", nil); err != nil {
		t.Fatalf("runFast: %v", err)
	}
	_, renames, _ := api.recorded()
	if len(renames) != 1 || renames[0] != "w1:t1=make -j all" {
		t.Fatalf("renames = %v, want [w1:t1=make -j all]", renames)
	}

	// The pane now reports another tab (moved out): don't rename.
	api.setPaneTitle("w1:p1", "w1:t9", "", "elsewhere")
	if err := runFast("precmd", nil); err != nil {
		t.Fatalf("runFast after move: %v", err)
	}
	_, renames, _ = api.recorded()
	if len(renames) != 1 {
		t.Fatalf("moved-out pane still renamed its old tab: %v", renames)
	}
}

// A transient tab.get failure during the hook's apply must escalate to a
// full pass rather than silently dropping the title: the linger exits
// quiet, and no later event will carry that title again. On the fake, no
// snapshot is configured, so the escalated full pass fails outright — that
// error surfacing from runFast is the proof escalation happened.
func TestRunFastDaemonlessEscalatesTabBlip(t *testing.T) {
	api := newFakeAPI(t)
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.hcl"),
		[]byte("template = \"x\"\ntabs {\n  terminal_titles = true\n  watch_titles = false\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", configDir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	t.Setenv("HERDR_SESSION", "insess")
	t.Setenv("HERDR_TAB_ID", "w1:t1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_SOCKET_PATH", api.sockPath)
	// The pane is known but its tab is not registered: tab.get blips.
	api.setPaneTitle("w1:p1", "w1:t1", "", "make -j all")

	err := runFast("precmd", nil)
	if err == nil {
		t.Fatal("tab.get blip was swallowed: runFast returned nil, want the escalated full pass's error")
	}
	_, renames, _ := api.recorded()
	if len(renames) != 0 {
		t.Fatalf("renames = %v, want none (tab.get failed)", renames)
	}
}

// Only precmd lingers: shells set the title at the prompt, so preexec sees
// the previous prompt's title, and a preexec subscriber would just double the
// hook processes per command for a window precmd's linger already covers.
func TestRunFastDaemonlessLingersOnlyAtPrecmd(t *testing.T) {
	api := newFakeAPI(t)
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.hcl"),
		[]byte("template = \"x\"\ntabs {\n  terminal_titles = true\n  watch_titles = false\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", configDir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	t.Setenv("HERDR_SESSION", "insess")
	t.Setenv("HERDR_TAB_ID", "w1:t1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_SOCKET_PATH", api.sockPath)
	api.setTab("w1:t1", "1")
	api.setPaneTitle("w1:p1", "w1:t1", "", "make -j all")

	subs := func() int {
		api.mu.Lock()
		defer api.mu.Unlock()
		return api.subscribes
	}
	if err := runFast("preexec", []string{"make -j all"}); err != nil {
		t.Fatalf("runFast preexec: %v", err)
	}
	if n := subs(); n != 0 {
		t.Fatalf("preexec subscribed %d times, want 0", n)
	}
	// preexec still applies the current title, it just doesn't linger.
	_, renames, _ := api.recorded()
	if len(renames) != 1 || renames[0] != "w1:t1=make -j all" {
		t.Fatalf("preexec renames = %v, want [w1:t1=make -j all]", renames)
	}
	if err := runFast("precmd", nil); err != nil {
		t.Fatalf("runFast precmd: %v", err)
	}
	if n := subs(); n != 1 {
		t.Fatalf("precmd subscribed %d times, want 1", n)
	}
}

func TestLingerPaneTitles(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	reader := bufio.NewReader(client)

	applies := 0
	apply := func() error {
		applies++
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- lingerPaneTitles(client, reader, "w1:p1",
			400*time.Millisecond, 5*time.Second, apply)
	}()

	write := func(s string) {
		t.Helper()
		if _, err := server.Write([]byte(s + "\n")); err != nil {
			t.Errorf("write: %v", err)
		}
	}
	// Every event resets the quiet window; when it finally elapses, a single
	// apply fires — events are only a change signal, so a burst (including a
	// fresh subscription's history replay) collapses into one re-read.
	write(`{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w1:p1","agent":"","focused":true,"terminal_title_stripped":"stale replay"}}}`)
	write(`{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w9:p9","agent":"","focused":true,"terminal_title_stripped":"other pane"}}}`)
	write(`not json`)
	write(`{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w1:p1","agent":"","focused":true,"terminal_title_stripped":"one"}}}`)
	time.Sleep(150 * time.Millisecond) // < quiet: the window resets, same linger
	write(`{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w1:p1","agent":"claude","focused":false,"terminal_title_stripped":"two"}}}`)

	select {
	case err := <-done: // the quiet window elapses with nothing more to read
		if err != nil {
			t.Fatalf("linger: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("linger did not end on a quiet stream")
	}
	if applies != 1 {
		t.Fatalf("applies = %d, want exactly 1 coalesced apply", applies)
	}
}

// Another pane's events neither trigger an apply nor extend the quiet
// window: a chatty neighbor must not hold this pane's linger open.
func TestLingerPaneTitlesIgnoresOtherPanes(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	reader := bufio.NewReader(client)

	applies := 0
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- lingerPaneTitles(client, reader, "w1:p1",
			200*time.Millisecond, 10*time.Second, func() error { applies++; return nil })
	}()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		other := `{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w9:p9","agent":"","focused":true,"terminal_title_stripped":"other"}}}` + "\n"
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = server.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
			if _, err := server.Write([]byte(other)); err != nil {
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("linger: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("linger did not end while another pane chattered")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("another pane's chatter extended the quiet window: %v", elapsed)
	}
	if applies != 0 {
		t.Fatalf("applies = %d, want none for another pane's events", applies)
	}
}

// The hard cap ends the linger even when events keep flowing — a
// title-spamming pane must not cause hook processes to live forever.
func TestLingerPaneTitlesCap(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	reader := bufio.NewReader(client)

	done := make(chan error, 1)
	go func() {
		done <- lingerPaneTitles(client, reader, "w1:p1", time.Second, 200*time.Millisecond,
			func() error { return nil })
	}()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ev := `{"event":"pane_updated","data":{"type":"pane_updated","pane":{"pane_id":"w1:p1","agent":"","focused":true,"terminal_title_stripped":"spam"}}}` + "\n"
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = server.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
			if _, err := server.Write([]byte(ev)); err != nil {
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cap did not end the linger under constant events")
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

	// terminal titles with the daemon (watch_titles defaults true). The
	// daemon only reacts to events, and herdr has no "foreground command
	// changed" event, so a program that sets NO title (helix, less, most CLI
	// tools) is invisible to it: the hook is the only thing that can name
	// the tab. So on a pane with no terminal title the hook still renames by
	// program. It must also still probe for a dead daemon; daemonAlive's
	// O_CREATE open leaves the lock file behind, so its reappearance proves
	// the probe ran.
	if err := os.WriteFile(filepath.Join(configDir, "config.hcl"),
		[]byte("template = \"x\"\ntabs { terminal_titles = true }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(stateDir, "watch.lock.fastsess")
	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("lock file missing before terminal-titles run: %v", err)
	}
	api.setPaneTitle("w1:p1", "w1:t1", "", "") // no terminal title (helix)
	run("preexec", "hx x")
	_, renames, _ = api.recorded()
	if len(renames) != before+1 || renames[before] != "w1:t1=hx" {
		t.Fatalf("terminal_titles=true, untitled pane: renames = %v, want trailing w1:t1=hx", renames)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("terminal-titles fast path skipped the daemon probe: %v", err)
	}
	before = len(renames)

	// A pane that DOES carry a terminal title belongs to the daemon's
	// pane.updated stream: a process-derived rename would only fight the
	// title, so the hook yields.
	api.setPaneTitle("w1:p1", "w1:t1", "", "user@host: ~/proj")
	run("preexec", "nvim x")
	_, renames, _ = api.recorded()
	if len(renames) != before {
		t.Fatalf("terminal_titles=true, titled pane: hook renamed instead of yielding: %v", renames)
	}

	// terminal titles without the daemon (watch_titles=false): the hook is
	// the only live naming path, so it keeps just the focused tab up to
	// date, leaving the rest to the watchdog events' full passes.
	noDaemonConfig := `
template = "x"
tabs {
  terminal_titles = true
  watch_titles    = false
}
`
	if err := os.WriteFile(filepath.Join(configDir, "config.hcl"), []byte(noDaemonConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	api.setPaneTitle("w1:p1", "w1:t1", "", "make -j all")
	run("precmd", "fish")
	_, renames, _ = api.recorded()
	if len(renames) != before+1 || renames[before] != "w1:t1=make -j all" {
		t.Fatalf("hook title rename => %v, want trailing w1:t1=make -j all", renames)
	}
	// No snapshot is configured on the fake, so a full reconcile would have
	// failed the run outright — the rename proves the path stayed targeted.

	// A hook event from an unfocused pane must not cause a rename.
	api.setPaneUnfocused("w1:p1")
	api.setTabShape("w1:t1", 2, true)
	api.setPaneTitle("w1:p1", "w1:t1", "", "sneaky background title")
	run("precmd", "fish")
	_, renames, _ = api.recorded()
	if len(renames) != before+1 {
		t.Fatalf("unfocused pane renamed a split: %v", renames)
	}
}
