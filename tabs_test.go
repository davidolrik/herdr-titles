package main

import (
	"path/filepath"
	"testing"
)

type tabsFixture struct {
	api    *fakeAPI
	cfg    *TabsConfig
	states TabStates
}

func newTabsFixture(t *testing.T) *tabsFixture {
	t.Helper()
	f := &tabsFixture{
		api:    newFakeAPI(t),
		cfg:    DefaultTabsConfig(),
		states: TabStates{},
	}
	f.cfg.ShellName = "zsh"
	return f
}

func (f *tabsFixture) renames(t *testing.T) []string {
	t.Helper()
	_, renames, _ := f.api.recorded()
	return renames
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
	f.api.setProcessInfo("w1:p1", "nvim", "nvim main.go")
	snap := singlePaneSnap("1")

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1=nvim" {
		t.Errorf("renames = %v, want [w1:t1=nvim]", got)
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
	f.api.setProcessInfo("w1:p1", "-/usr/local/bin/zsh", "-zsh")
	snap := singlePaneSnap("")

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1=zsh" {
		t.Errorf("renames = %v, want [w1:t1=zsh]", got)
	}
}

func TestReconcileTabsAgentTitle(t *testing.T) {
	f := newTabsFixture(t)
	f.cfg.Icons.Enabled = true
	snap := singlePaneSnap("2")
	snap.Agents = []Agent{{PaneID: "w1:p1", Kind: "claude", Status: "working", Title: "Fix flaky test"}}

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	want := "w1:t1=\U000F06A9 Fix flaky test"
	if got := f.renames(t); len(got) != 1 || got[0] != want {
		t.Errorf("renames = %v, want [%s]", got, want)
	}
	// The agent path must not consult process-info at all.
	f.api.mu.Lock()
	infoReqs := f.api.infoReqs
	f.api.mu.Unlock()
	if infoReqs != 0 {
		t.Errorf("agent-title path made %d process-info requests", infoReqs)
	}
}

func TestReconcileTabsAgentTitleDisabledFallsBack(t *testing.T) {
	f := newTabsFixture(t)
	f.cfg.AgentTitles = false
	f.cfg.TerminalTitles = true
	f.api.setProcessInfo("w1:p1", "claude", "claude --resume")
	snap := singlePaneSnap("")
	snap.Agents = []Agent{{PaneID: "w1:p1", Kind: "claude", Status: "working", Title: "Fix flaky test"}}
	// The agent's session title doubles as the pane's terminal title; the
	// terminal-title path must not resurrect it when agent_titles is off.
	snap.Panes[0].Title = "Fix flaky test"

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1=claude" {
		t.Errorf("renames = %v, want [w1:t1=claude]", got)
	}
}

func TestReconcileTabsAgentWithoutTitleFallsBack(t *testing.T) {
	f := newTabsFixture(t)
	f.api.setProcessInfo("w1:p1", "claude", "claude")
	snap := singlePaneSnap("")
	snap.Agents = []Agent{{PaneID: "w1:p1", Kind: "claude", Status: "working", Title: ""}}

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1=claude" {
		t.Errorf("renames = %v, want [w1:t1=claude]", got)
	}
}

func TestReconcileTabsUnwrapsInterpreter(t *testing.T) {
	f := newTabsFixture(t)
	f.cfg.Icons.Enabled = true
	// The argv shape of a Python console script: argv0 is the interpreter,
	// the tool actually run is argv[1].
	f.api.setProcessInfoArgv("w1:p1", "python", []string{
		"/Volumes/Projects/infra/.venv/bin/python",
		"/Volumes/Projects/infra/.venv/bin/ansible-playbook",
		"server-upgrade.yml",
	})
	snap := singlePaneSnap("")

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	want := "w1:t1=\U000F109A ansible-playbook"
	if got := f.renames(t); len(got) != 1 || got[0] != want {
		t.Errorf("renames = %v, want [%s]", got, want)
	}
}

func TestReconcileTabsTerminalTitle(t *testing.T) {
	f := newTabsFixture(t)
	f.cfg.TerminalTitles = true
	snap := singlePaneSnap("1")
	snap.Panes[0].Title = "make -j all"

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1=make -j all" {
		t.Errorf("renames = %v, want [w1:t1=make -j all]", got)
	}
	// The terminal-title path must not consult process-info at all.
	f.api.mu.Lock()
	infoReqs := f.api.infoReqs
	f.api.mu.Unlock()
	if infoReqs != 0 {
		t.Errorf("terminal-title path made %d process-info requests", infoReqs)
	}
}

func TestReconcileTabsAgentTitleBeatsTerminalTitle(t *testing.T) {
	f := newTabsFixture(t)
	f.cfg.TerminalTitles = true
	snap := singlePaneSnap("")
	snap.Agents = []Agent{{PaneID: "w1:p1", Kind: "claude", Status: "working", Title: "Fix flaky test"}}
	snap.Panes[0].Title = "claude"

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1=Fix flaky test" {
		t.Errorf("renames = %v, want the agent title", got)
	}
}

func TestReconcileTabsTerminalTitleDisabledFallsBack(t *testing.T) {
	f := newTabsFixture(t)
	f.cfg.TerminalTitles = false
	f.api.setProcessInfo("w1:p1", "nvim", "nvim")
	snap := singlePaneSnap("")
	snap.Panes[0].Title = "make -j all"

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1=nvim" {
		t.Errorf("renames = %v, want [w1:t1=nvim]", got)
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
	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")
	if got := f.renames(t); len(got) != 0 {
		t.Errorf("background multi-pane tab renamed: %v", got)
	}
}

func TestReconcileTabsFocusedMultiPaneUsesFocusedPane(t *testing.T) {
	f := newTabsFixture(t)
	f.api.setProcessInfo("w1:p2", "nvim", "nvim")
	snap := &Snapshot{
		FocusedTabID: "w1:t1",
		Tabs:         []Tab{{TabID: "w1:t1", Label: "", PaneCount: 2, Focused: true}},
		Panes: []Pane{
			{PaneID: "w1:p1", TabID: "w1:t1"},
			{PaneID: "w1:p2", TabID: "w1:t1", Focused: true},
		},
	}
	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")
	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1=nvim" {
		t.Errorf("renames = %v, want focused pane's program", got)
	}
}

func TestReconcileTabsHandNamedOptsOut(t *testing.T) {
	f := newTabsFixture(t)
	f.api.setProcessInfo("w1:p1", "nvim", "nvim")
	snap := singlePaneSnap("my precious tab")

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

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

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	f.api.mu.Lock()
	total := f.api.infoReqs + len(f.api.renames)
	f.api.mu.Unlock()
	if total != 0 {
		t.Errorf("opted-out tab caused %d API calls", total)
	}
}

func TestReconcileTabsProcessInfoFailureLeavesTabAlone(t *testing.T) {
	f := newTabsFixture(t)
	// no process-info registered -> the fake answers with an error
	snap := singlePaneSnap("nvim")
	f.states["w1:t1"] = TabState{Auto: "nvim", Enabled: true}

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 0 {
		t.Errorf("blip renamed tab: %v", got)
	}
	if st := f.states["w1:t1"]; !st.Enabled || st.Auto != "nvim" {
		t.Errorf("blip disturbed state: %+v", st)
	}
}

func TestReconcileTabsSkipsRenameWhenCorrect(t *testing.T) {
	f := newTabsFixture(t)
	f.api.setProcessInfo("w1:p1", "nvim", "nvim")
	snap := singlePaneSnap("nvim")
	f.states["w1:t1"] = TabState{Auto: "nvim", Enabled: true}

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 0 {
		t.Errorf("correct label re-renamed: %v", got)
	}
}

func TestReconcileTabsPrunesClosedTabs(t *testing.T) {
	f := newTabsFixture(t)
	f.api.setProcessInfo("w1:p1", "nvim", "nvim")
	f.states["gone:t9"] = TabState{Auto: "old", Enabled: true}
	snap := singlePaneSnap("")

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	if _, ok := f.states["gone:t9"]; ok {
		t.Error("closed tab's state not pruned")
	}
}

func TestRenameTabForTitleAgent(t *testing.T) {
	api := newFakeAPI(t)
	statePath := filepath.Join(t.TempDir(), "tabstate.test.json")
	cfg := DefaultTabsConfig()
	cfg.ShellName = "zsh"
	cfg.Icons.Enabled = true
	api.setTab("w1:t1", "\U000F06A9 Old title")
	if err := SaveTabStates(statePath, TabStates{"w1:t1": {Auto: "\U000F06A9 Old title", Enabled: true}}); err != nil {
		t.Fatal(err)
	}

	if err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "claude", "New title", cfg); err != nil {
		t.Fatal(err)
	}
	_, renames, _ := api.recorded()
	want := "w1:t1=\U000F06A9 New title"
	if len(renames) != 1 || renames[0] != want {
		t.Fatalf("renames = %v, want [%s]", renames, want)
	}
	if st := LoadTabStates(statePath)["w1:t1"]; st.Auto != "\U000F06A9 New title" || !st.Enabled {
		t.Errorf("state = %+v, want owned new title", st)
	}

	// Opted-out tabs stay untouched; empty titles are ignored.
	if err := SaveTabStates(statePath, TabStates{"w1:t1": {Enabled: false}}); err != nil {
		t.Fatal(err)
	}
	if err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "claude", "Another", cfg); err != nil {
		t.Fatal(err)
	}
	if err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "claude", "", cfg); err != nil {
		t.Fatal(err)
	}
	_, renames, _ = api.recorded()
	if len(renames) != 1 {
		t.Errorf("opted-out/empty-title caused renames: %v", renames)
	}
}

func TestRenameTabForTitlePlainPane(t *testing.T) {
	api := newFakeAPI(t)
	statePath := filepath.Join(t.TempDir(), "tabstate.test.json")
	cfg := DefaultTabsConfig()
	cfg.ShellName = "zsh"
	cfg.MaxNameLen = 10
	cfg.TerminalTitles = true
	// Icons must not apply to plain pane titles even when enabled.
	cfg.Icons.Enabled = true
	api.setTab("w1:t1", "zsh")
	if err := SaveTabStates(statePath, TabStates{"w1:t1": {Auto: "zsh", Enabled: true}}); err != nil {
		t.Fatal(err)
	}

	if err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "", "make -j all target", cfg); err != nil {
		t.Fatal(err)
	}
	_, renames, _ := api.recorded()
	if len(renames) != 1 || renames[0] != "w1:t1=make -j al" {
		t.Fatalf("renames = %v, want [w1:t1=make -j al] (truncated at max_name_len)", renames)
	}
	if st := LoadTabStates(statePath)["w1:t1"]; st.Auto != "make -j al" || !st.Enabled {
		t.Errorf("state = %+v, want owned pane title", st)
	}

	// terminal_titles = false gates the plain-pane path but not the agent path.
	cfg.TerminalTitles = false
	if err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "", "other title", cfg); err != nil {
		t.Fatal(err)
	}
	_, renames, _ = api.recorded()
	if len(renames) != 1 {
		t.Errorf("terminal_titles=false still renamed: %v", renames)
	}
}
