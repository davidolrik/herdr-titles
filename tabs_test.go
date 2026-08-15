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
	f.api.setProcessInfo("w1:p1", "claude", "claude --resume")
	snap := singlePaneSnap("")
	snap.Agents = []Agent{{PaneID: "w1:p1", Kind: "claude", Status: "working", Title: "Fix flaky test"}}
	// The agent's session title doubles as the pane's terminal title; with
	// terminal_titles off too, the program name is all that remains.
	snap.Panes[0].Title = "Fix flaky test"

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1=claude" {
		t.Errorf("renames = %v, want [w1:t1=claude]", got)
	}
}

func TestReconcileTabsAgentTitleDisabledUsesTerminalTitle(t *testing.T) {
	f := newTabsFixture(t)
	f.cfg.AgentTitles = false
	f.cfg.TerminalTitles = true
	snap := singlePaneSnap("")
	snap.Agents = []Agent{{PaneID: "w1:p1", Kind: "claude", Status: "working", Title: "Fix flaky test"}}
	snap.Panes[0].Title = "Fix flaky test"

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1=Fix flaky test" {
		t.Errorf("renames = %v, want the terminal title, plain-formatted", got)
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

func TestRenameTabForTitleClearFallsBackToProgram(t *testing.T) {
	api := newFakeAPI(t)
	statePath := filepath.Join(t.TempDir(), "tabstate.test.json")
	cfg := DefaultTabsConfig()
	cfg.ShellName = "zsh"
	cfg.TerminalTitles = true
	api.setTab("w1:t1", "make -j all")
	api.setProcessInfo("w1:p1", "nvim", "nvim main.go")
	if err := SaveTabStates(statePath, TabStates{"w1:t1": {Auto: "make -j all", Enabled: true}}); err != nil {
		t.Fatal(err)
	}

	if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "", "", true, cfg); err != nil {
		t.Fatal(err)
	}
	_, renames, _ := api.recorded()
	if len(renames) != 1 || renames[0] != "w1:t1=nvim" {
		t.Fatalf("renames = %v, want [w1:t1=nvim] (fallback to program)", renames)
	}
}

// Unknown focus (classifier state cold): a single-pane tab is renamed since
// its sole pane is trivially the right one; a multi-pane tab is not renamed
// to avoid naming ownership bouncing between panes.
func TestRenameTabForTitleUnknownFocus(t *testing.T) {
	api := newFakeAPI(t)
	statePath := filepath.Join(t.TempDir(), "tabstate.test.json")
	cfg := DefaultTabsConfig()
	cfg.ShellName = "zsh"
	cfg.TerminalTitles = true
	api.setTab("w1:t1", "zsh")
	api.setProcessInfo("w1:p1", "nvim", "nvim")
	if err := SaveTabStates(statePath, TabStates{"w1:t1": {Auto: "zsh", Enabled: true}}); err != nil {
		t.Fatal(err)
	}

	if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "", "make", false, cfg); err != nil {
		t.Fatal(err)
	}
	_, renames, _ := api.recorded()
	if len(renames) != 1 || renames[0] != "w1:t1=make" {
		t.Fatalf("single-pane unknown focus => %v, want [w1:t1=make]", renames)
	}

	api.setTabShape("w1:t1", 2, true)
	if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "", "other", false, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "", "", false, cfg); err != nil {
		t.Fatal(err)
	}
	_, renames, _ = api.recorded()
	api.mu.Lock()
	infoReqs := api.infoReqs
	api.mu.Unlock()
	if len(renames) != 1 || infoReqs != 0 {
		t.Errorf("multi-pane with unknown focus acted: renames=%v infoReqs=%d", renames, infoReqs)
	}

	// Known focus
	if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "", "other", true, cfg); err != nil {
		t.Fatal(err)
	}
	_, renames, _ = api.recorded()
	if len(renames) != 2 || renames[1] != "w1:t1=other" {
		t.Errorf("multi-pane with known focus => %v, want trailing w1:t1=other", renames)
	}
}

// A clear on a background multi-pane tab does not fall back to the foreground
// program even with the focus known, mirroring the full pass: the label keeps
// its last meaningful value until the tab is refocused.
func TestRenameTabForTitleClearBackgroundMulti(t *testing.T) {
	api := newFakeAPI(t)
	statePath := filepath.Join(t.TempDir(), "tabstate.test.json")
	cfg := DefaultTabsConfig()
	cfg.ShellName = "zsh"
	cfg.TerminalTitles = true
	api.setTab("w1:t1", "make")
	api.setTabShape("w1:t1", 2, false)
	api.setProcessInfo("w1:p1", "zsh", "zsh")
	if err := SaveTabStates(statePath, TabStates{"w1:t1": {Auto: "make", Enabled: true}}); err != nil {
		t.Fatal(err)
	}

	if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "", "", true, cfg); err != nil {
		t.Fatal(err)
	}
	_, renames, _ := api.recorded()
	api.mu.Lock()
	infoReqs := api.infoReqs
	api.mu.Unlock()
	if len(renames) != 0 || infoReqs != 0 {
		t.Errorf("background split clear acted: renames=%v infoReqs=%d", renames, infoReqs)
	}
}

// With both title modes off, the daemon has no mandate over the tab: neither
// a set nor a clear may touch the API — not even the clear's process-info
// fallback. A transient error while a rename was due must report retryFull:
// the event will not be re-emitted, so only an escalated full pass can
// recover the title.
func TestRenameTabForTitleRetryForTransientError(t *testing.T) {
	api := newFakeAPI(t)
	statePath := filepath.Join(t.TempDir(), "tabstate.test.json")
	cfg := DefaultTabsConfig()
	cfg.ShellName = "zsh"
	cfg.TerminalTitles = true

	// tab.get error: the tab is not registered on the fake.
	retry, err := RenameTabForTitle(api.sockPath, statePath, "w9:t9", "w9:p9", "", "make", true, cfg)
	if err != nil || !retry {
		t.Errorf("tab.get blip => retry=%v err=%v, want retry", retry, err)
	}

	// Clear-fallback error: tab known, but no process info for the pane.
	api.setTab("w1:t1", "make")
	retry, err = RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "", "", true, cfg)
	if err != nil || !retry {
		t.Errorf("process-info blip => retry=%v err=%v, want retry", retry, err)
	}

	// A policy decline is not a transient error.
	api.setTabShape("w1:t1", 2, true)
	retry, err = RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "", "make", false, cfg)
	if err != nil || retry {
		t.Errorf("policy decline => retry=%v err=%v, want no retry", retry, err)
	}
}

func TestRenameTabForTitleBothModesOff(t *testing.T) {
	api := newFakeAPI(t)
	statePath := filepath.Join(t.TempDir(), "tabstate.test.json")
	cfg := DefaultTabsConfig()
	cfg.ShellName = "zsh"
	cfg.AgentTitles = false
	cfg.TerminalTitles = false
	api.setTab("w1:t1", "zsh")
	api.setProcessInfo("w1:p1", "nvim", "nvim")

	for _, title := range []string{"Session title", ""} {
		if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "claude", title, true, cfg); err != nil {
			t.Fatal(err)
		}
	}
	// The master switch off is an even earlier no-op, whatever the modes say.
	cfg.Enabled = false
	cfg.AgentTitles = true
	cfg.TerminalTitles = true
	for _, title := range []string{"Session title", ""} {
		if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "claude", title, true, cfg); err != nil {
			t.Fatal(err)
		}
	}
	_, renames, _ := api.recorded()
	api.mu.Lock()
	infoReqs := api.infoReqs
	api.mu.Unlock()
	if len(renames) != 0 || infoReqs != 0 {
		t.Errorf("gated-off config still touched the API: renames=%v infoReqs=%d", renames, infoReqs)
	}
}

// A clear whose fallback program is the shell renames to the empty label
// under hide_shell, handing the tab back to herdr's numbering.
func TestRenameTabForTitleClearHideShell(t *testing.T) {
	api := newFakeAPI(t)
	statePath := filepath.Join(t.TempDir(), "tabstate.test.json")
	cfg := DefaultTabsConfig()
	cfg.ShellName = "zsh"
	cfg.TerminalTitles = true
	cfg.HideShell = true
	api.setTab("w1:t1", "make")
	api.setProcessInfo("w1:p1", "zsh", "zsh")
	if err := SaveTabStates(statePath, TabStates{"w1:t1": {Auto: "make", Enabled: true}}); err != nil {
		t.Fatal(err)
	}

	if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "", "", true, cfg); err != nil {
		t.Fatal(err)
	}
	_, renames, _ := api.recorded()
	if len(renames) != 1 || renames[0] != "w1:t1=" {
		t.Fatalf("renames = %v, want [w1:t1=] (hide_shell empty label)", renames)
	}
	if st := LoadTabStates(statePath)["w1:t1"]; st.Auto != "" || !st.Enabled {
		t.Errorf("state = %+v, want owned empty label", st)
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

// backgroundSplitSnap is a background two-pane tab whose layout remembers
// w1:p2 as its focused pane.
func backgroundSplitSnap() *Snapshot {
	return &Snapshot{
		FocusedTabID: "w1:t2",
		Tabs:         []Tab{{TabID: "w1:t1", Label: "1", PaneCount: 2, Focused: false}},
		Panes: []Pane{
			{PaneID: "w1:p1", TabID: "w1:t1"},
			{PaneID: "w1:p2", TabID: "w1:t1"},
		},
		TabFocus: map[string]string{"w1:t1": "w1:p2"},
	}
}

// A background split is named from its remembered pane's TITLE — free from
// the snapshot and genuinely fresh, since titles are evented.
func TestReconcileTabsBackgroundMultiPaneUsesRememberedFocus(t *testing.T) {
	f := newTabsFixture(t)
	f.cfg.TerminalTitles = true
	snap := backgroundSplitSnap()
	snap.Panes[1].Title = "make -j all"

	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")

	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1=make -j all" {
		t.Errorf("renames = %v, want the remembered focused pane's title", got)
	}
	f.api.mu.Lock()
	infoReqs := f.api.infoReqs
	f.api.mu.Unlock()
	if infoReqs != 0 {
		t.Errorf("background split cost %d process-info requests", infoReqs)
	}
}

// Without a usable title, a background split is left alone: process-info is
// not evented, so polling it buys nothing, and the label keeps its last
// meaningful value until the tab is refocused (the full pass on focus
// recomputes the program name).
func TestReconcileTabsBackgroundMultiPaneSkipsProcessInfo(t *testing.T) {
	f := newTabsFixture(t)
	f.api.setProcessInfo("w1:p2", "nvim", "nvim")

	ReconcileTabs(f.api.sockPath, backgroundSplitSnap(), f.cfg, f.states, "")

	f.api.mu.Lock()
	infoReqs := f.api.infoReqs
	f.api.mu.Unlock()
	if got := f.renames(t); len(got) != 0 || infoReqs != 0 {
		t.Errorf("background split touched program naming: renames=%v infoReqs=%d", got, infoReqs)
	}
}

// The tab layout's remembered pane disagrees with the globally focused pane.
// The layout wins, since herdr is the source of truth.
func TestReconcileTabsFocusedMultiPaneLayoutFocusWins(t *testing.T) {
	f := newTabsFixture(t)
	f.api.setProcessInfo("w1:p1", "htop", "htop")
	snap := &Snapshot{
		FocusedTabID: "w1:t1",
		Tabs:         []Tab{{TabID: "w1:t1", Label: "", PaneCount: 2, Focused: true}},
		Panes: []Pane{
			{PaneID: "w1:p1", TabID: "w1:t1"},
			{PaneID: "w1:p2", TabID: "w1:t1", Focused: true},
		},
		TabFocus: map[string]string{"w1:t1": "w1:p1"},
	}
	ReconcileTabs(f.api.sockPath, snap, f.cfg, f.states, "")
	if got := f.renames(t); len(got) != 1 || got[0] != "w1:t1=htop" {
		t.Errorf("renames = %v, want the layout-remembered pane's program", got)
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

	if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "claude", "New title", true, cfg); err != nil {
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

	// Opted-out tabs stay untouched; a cleared title with no process info
	// available (the fake has none for this pane) is a no-op blip.
	if err := SaveTabStates(statePath, TabStates{"w1:t1": {Enabled: false}}); err != nil {
		t.Fatal(err)
	}
	if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "claude", "Another", true, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "claude", "", true, cfg); err != nil {
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

	if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "", "make -j all target", true, cfg); err != nil {
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
	if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "", "other title", true, cfg); err != nil {
		t.Fatal(err)
	}
	_, renames, _ = api.recorded()
	if len(renames) != 1 {
		t.Errorf("terminal_titles=false still renamed: %v", renames)
	}

	// agent_titles off + terminal_titles on: an agent's session title counts
	// as an ordinary terminal title (plain format, max_name_len).
	cfg.AgentTitles = false
	cfg.TerminalTitles = true
	if _, err := RenameTabForTitle(api.sockPath, statePath, "w1:t1", "w1:p1", "claude", "agent session name", true, cfg); err != nil {
		t.Fatal(err)
	}
	_, renames, _ = api.recorded()
	if len(renames) != 2 || renames[1] != "w1:t1=agent sess" {
		t.Errorf("renames = %v, want trailing plain-formatted agent title", renames)
	}
}
