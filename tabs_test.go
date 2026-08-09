package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTabHerdr writes a herdr stand-in that records every invocation and
// serves `pane process-info` responses from per-pane JSON files in infoDir.
func fakeTabHerdr(t *testing.T, dir, callsPath, infoDir string) string {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
if [ "$1" = "pane" ] && [ "$2" = "process-info" ]; then
  pane=$(printf '%%s' "$4" | tr ':' '_')
  cat %q/"$pane".json 2>/dev/null || exit 1
fi
`, callsPath, infoDir)
	path := filepath.Join(dir, "herdr")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// processInfo writes a process-info fixture whose group leader runs program.
func processInfo(t *testing.T, infoDir, paneID, argv0, cmdline string) {
	t.Helper()
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"result":{"process_info":{
	  "foreground_process_group_id": 100,
	  "foreground_processes": [
	    {"pid": 100, "argv0": %q, "cmdline": %q, "name": "on-disk-name"},
	    {"pid": 101, "argv0": "child", "cmdline": "child", "name": "child"}
	  ]}}}`, argv0, cmdline)
	path := filepath.Join(infoDir, strings.ReplaceAll(paneID, ":", "_")+".json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type tabsFixture struct {
	herdrBin string
	calls    string
	infoDir  string
	cfg      *TabsConfig
	states   TabStates
}

func newTabsFixture(t *testing.T) *tabsFixture {
	t.Helper()
	dir := t.TempDir()
	f := &tabsFixture{
		calls:   filepath.Join(dir, "calls.log"),
		infoDir: filepath.Join(dir, "info"),
		cfg:     DefaultTabsConfig(),
		states:  TabStates{},
	}
	f.cfg.ShellName = "zsh"
	f.herdrBin = fakeTabHerdr(t, dir, f.calls, f.infoDir)
	return f
}

func (f *tabsFixture) renames(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, line := range readCalls(t, f.calls) {
		if strings.HasPrefix(line, "tab rename ") {
			out = append(out, strings.TrimPrefix(line, "tab rename "))
		}
	}
	return out
}

func singlePaneSnap(label string) *Snapshot {
	return &Snapshot{
		FocusedWorkspaceID: "w1",
		FocusedTabID:       "w1:t1",
		TabLabel:           label,
		Tabs:               []Tab{{TabID: "w1:t1", WorkspaceID: "w1", Label: label, PaneCount: 1, Focused: true}},
		Panes:              []Pane{{PaneID: "w1:p1", TabID: "w1:t1", Focused: true}},
	}
}

func TestReconcileTabsNamesSinglePane(t *testing.T) {
	f := newTabsFixture(t)
	processInfo(t, f.infoDir, "w1:p1", "nvim", "nvim main.go")
	snap := singlePaneSnap("1")

	ReconcileTabs(f.herdrBin, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1 nvim" {
		t.Errorf("renames = %v, want [w1:t1 nvim]", got)
	}
	if st := f.states["w1:t1"]; !st.Enabled || st.Auto != "nvim" {
		t.Errorf("state = %+v, want owned nvim", st)
	}
	if snap.TabLabel != "nvim" {
		t.Errorf("focused TabLabel not updated: %q", snap.TabLabel)
	}
}

func TestReconcileTabsStripsLoginDashAndPath(t *testing.T) {
	f := newTabsFixture(t)
	processInfo(t, f.infoDir, "w1:p1", "-/usr/local/bin/zsh", "-zsh")
	snap := singlePaneSnap("")

	ReconcileTabs(f.herdrBin, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1 zsh" {
		t.Errorf("renames = %v, want [w1:t1 zsh]", got)
	}
}

func TestReconcileTabsAgentTitle(t *testing.T) {
	f := newTabsFixture(t)
	f.cfg.Icons.Enabled = true
	snap := singlePaneSnap("2")
	snap.Agents = []Agent{{PaneID: "w1:p1", Kind: "claude", Status: "working", Title: "Fix flaky test"}}

	ReconcileTabs(f.herdrBin, snap, f.cfg, f.states, "")

	want := "\U000F06A9 Fix flaky test"
	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1 "+want {
		t.Errorf("renames = %v, want [w1:t1 %s]", got, want)
	}
	// The agent path must not consult process-info at all.
	for _, line := range readCalls(t, f.calls) {
		if strings.Contains(line, "process-info") {
			t.Errorf("unexpected process-info call: %s", line)
		}
	}
}

func TestReconcileTabsAgentTitleDisabledFallsBack(t *testing.T) {
	f := newTabsFixture(t)
	f.cfg.AgentTitles = false
	processInfo(t, f.infoDir, "w1:p1", "claude", "claude --resume")
	snap := singlePaneSnap("")
	snap.Agents = []Agent{{PaneID: "w1:p1", Kind: "claude", Status: "working", Title: "Fix flaky test"}}

	ReconcileTabs(f.herdrBin, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1 claude" {
		t.Errorf("renames = %v, want [w1:t1 claude]", got)
	}
}

func TestReconcileTabsAgentWithoutTitleFallsBack(t *testing.T) {
	f := newTabsFixture(t)
	processInfo(t, f.infoDir, "w1:p1", "claude", "claude")
	snap := singlePaneSnap("")
	snap.Agents = []Agent{{PaneID: "w1:p1", Kind: "claude", Status: "working", Title: ""}}

	ReconcileTabs(f.herdrBin, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1 claude" {
		t.Errorf("renames = %v, want [w1:t1 claude]", got)
	}
}

func TestReconcileTabsBackgroundMultiPaneUntouched(t *testing.T) {
	f := newTabsFixture(t)
	snap := &Snapshot{
		FocusedTabID: "w1:t1",
		Tabs: []Tab{
			{TabID: "w1:t1", Label: "keep", PaneCount: 2, Focused: false},
		},
		Panes: []Pane{
			{PaneID: "w1:p1", TabID: "w1:t1"},
			{PaneID: "w1:p2", TabID: "w1:t1"},
		},
	}
	ReconcileTabs(f.herdrBin, snap, f.cfg, f.states, "")
	if got := f.renames(t); len(got) != 0 {
		t.Errorf("background multi-pane tab renamed: %v", got)
	}
}

func TestReconcileTabsFocusedMultiPaneUsesFocusedPane(t *testing.T) {
	f := newTabsFixture(t)
	processInfo(t, f.infoDir, "w1:p2", "nvim", "nvim")
	snap := &Snapshot{
		FocusedTabID: "w1:t1",
		Tabs:         []Tab{{TabID: "w1:t1", Label: "", PaneCount: 2, Focused: true}},
		Panes: []Pane{
			{PaneID: "w1:p1", TabID: "w1:t1"},
			{PaneID: "w1:p2", TabID: "w1:t1", Focused: true},
		},
	}
	ReconcileTabs(f.herdrBin, snap, f.cfg, f.states, "")
	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1 nvim" {
		t.Errorf("renames = %v, want focused pane's program", got)
	}
}

func TestReconcileTabsHandNamedOptsOut(t *testing.T) {
	f := newTabsFixture(t)
	processInfo(t, f.infoDir, "w1:p1", "nvim", "nvim")
	snap := singlePaneSnap("my precious tab")

	ReconcileTabs(f.herdrBin, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 0 {
		t.Errorf("hand-named tab renamed: %v", got)
	}
	if st, ok := f.states["w1:t1"]; !ok || st.Enabled {
		t.Errorf("state = %+v, want opted out", f.states["w1:t1"])
	}
}

func TestReconcileTabsOptedOutSkipsProcessInfo(t *testing.T) {
	f := newTabsFixture(t)
	f.states["w1:t1"] = TabState{Enabled: false}
	snap := singlePaneSnap("my precious tab")

	ReconcileTabs(f.herdrBin, snap, f.cfg, f.states, "")

	if calls := readCalls(t, f.calls); len(calls) != 0 {
		t.Errorf("opted-out tab caused herdr calls: %v", calls)
	}
}

func TestReconcileTabsProcessInfoFailureLeavesTabAlone(t *testing.T) {
	f := newTabsFixture(t)
	// no process-info fixture -> fake herdr exits 1
	snap := singlePaneSnap("nvim")
	f.states["w1:t1"] = TabState{Auto: "nvim", Enabled: true}

	ReconcileTabs(f.herdrBin, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 0 {
		t.Errorf("blip renamed tab: %v", got)
	}
	if st := f.states["w1:t1"]; !st.Enabled || st.Auto != "nvim" {
		t.Errorf("blip disturbed state: %+v", st)
	}
}

func TestReconcileTabsSkipsRenameWhenCorrect(t *testing.T) {
	f := newTabsFixture(t)
	processInfo(t, f.infoDir, "w1:p1", "nvim", "nvim")
	snap := singlePaneSnap("nvim")
	f.states["w1:t1"] = TabState{Auto: "nvim", Enabled: true}

	ReconcileTabs(f.herdrBin, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 0 {
		t.Errorf("correct label re-renamed: %v", got)
	}
}

func TestReconcileTabsPrunesClosedTabs(t *testing.T) {
	f := newTabsFixture(t)
	processInfo(t, f.infoDir, "w1:p1", "nvim", "nvim")
	f.states["gone:t9"] = TabState{Auto: "old", Enabled: true}
	snap := singlePaneSnap("")

	ReconcileTabs(f.herdrBin, snap, f.cfg, f.states, "")

	if _, ok := f.states["gone:t9"]; ok {
		t.Error("closed tab's state not pruned")
	}
}
