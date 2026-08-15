package main

// Tab-name reconciliation, ported (minus jump-key numbering) from
// automatic-rename.sh of qu8n/herdr-automatic-rename (MIT, (c) Quan Nguyen),
// plus the agent-title mode: a pane hosting a recognized agent can name its
// tab after the agent's session title instead of the process name.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// activePane resolves which pane a tab is named after: a single-pane tab by
// its sole pane whether or not it is focused; a multi-pane tab by its
// layout's remembered focused pane (the pane herdr re-focuses when the tab
// is next selected). Without layout data (older herdr), a focused multi-pane
// tab falls back to the globally focused pane and a background tab to none.
func activePane(tab Tab, snap *Snapshot) string {
	var tabPanes []Pane
	for _, p := range snap.Panes {
		if p.TabID == tab.TabID {
			tabPanes = append(tabPanes, p)
		}
	}
	if len(tabPanes) == 0 {
		return ""
	}
	if tab.PaneCount == 1 {
		return tabPanes[0].PaneID
	}
	if paneID := snap.TabFocus[tab.TabID]; paneID != "" {
		return paneID
	}
	if tab.Focused {
		for _, p := range snap.Panes {
			if p.Focused {
				return p.PaneID
			}
		}
		return tabPanes[0].PaneID
	}
	return ""
}

// paneProgram fetches the foreground process-group leader of a pane. The
// argv0 -> argv[0] -> name precedence is load-bearing: .name is the on-disk
// executable, which reports a version string for claude and a ".<prog>-wrapped"
// on NixOS. A leading "-" (login shell) and any path prefix are stripped.
func paneProgram(sockPath, paneID string) (prog, cmdline string, err error) {
	result, err := apiRequest(sockPath, "pane.process_info", map[string]string{"pane_id": paneID})
	if err != nil {
		return "", "", err
	}

	var payload struct {
		ProcessInfo struct {
			ForegroundProcessGroupID int `json:"foreground_process_group_id"`
			ForegroundProcesses      []struct {
				PID     int      `json:"pid"`
				Argv0   string   `json:"argv0"`
				Argv    []string `json:"argv"`
				Cmdline string   `json:"cmdline"`
				Name    string   `json:"name"`
			} `json:"foreground_processes"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return "", "", fmt.Errorf("pane.process_info %s: %w", paneID, err)
	}

	info := payload.ProcessInfo
	for _, p := range info.ForegroundProcesses {
		if p.PID != info.ForegroundProcessGroupID {
			continue
		}
		prog = p.Argv0
		if prog == "" && len(p.Argv) > 0 {
			prog = p.Argv[0]
		}
		if prog == "" {
			prog = p.Name
		}
		prog = strings.TrimPrefix(prog, "-")
		if i := strings.LastIndex(prog, "/"); i >= 0 {
			prog = prog[i+1:]
		}
		prog = unwrapInterpreter(prog, p.Argv)
		cmdline = p.Cmdline
		if cmdline == "" {
			cmdline = strings.Join(p.Argv, " ")
		}
		return prog, cmdline, nil
	}
	return "", "", fmt.Errorf("pane.process_info %s: no group leader", paneID)
}

// tabInfo fetches a tab's current label, pane count, and whether it is the
// globally focused tab. ok=false when the call fails or returns no tab.
func tabInfo(sockPath, tabID string) (label string, paneCount int, focused, ok bool) {
	result, err := apiRequest(sockPath, "tab.get", map[string]string{"tab_id": tabID})
	if err != nil {
		return "", 0, false, false
	}
	var payload struct {
		Tab *struct {
			Label     string `json:"label"`
			PaneCount int    `json:"pane_count"`
			Focused   bool   `json:"focused"`
		} `json:"tab"`
	}
	if err := json.Unmarshal(result, &payload); err != nil || payload.Tab == nil {
		return "", 0, false, false
	}
	return payload.Tab.Label, payload.Tab.PaneCount, payload.Tab.Focused, true
}

// tabLabel fetches just a tab's current label (the shell-hook fast path).
func tabLabel(sockPath, tabID string) (string, bool) {
	label, _, _, ok := tabInfo(sockPath, tabID)
	return label, ok
}

// paneInfo fetches one pane's current state via pane.get (the shell-hook
// path under terminal_titles without a daemon).
func paneInfo(sockPath, paneID string) (Pane, bool) {
	result, err := apiRequest(sockPath, "pane.get", map[string]string{"pane_id": paneID})
	if err != nil {
		return Pane{}, false
	}
	var payload struct {
		Pane *struct {
			PaneID  string `json:"pane_id"`
			TabID   string `json:"tab_id"`
			Agent   string `json:"agent"`
			Focused bool   `json:"focused"`
			Title   string `json:"terminal_title_stripped"`
		} `json:"pane"`
	}
	if err := json.Unmarshal(result, &payload); err != nil || payload.Pane == nil {
		return Pane{}, false
	}
	p := payload.Pane
	return Pane{
		PaneID: p.PaneID, TabID: p.TabID, Agent: p.Agent,
		Focused: p.Focused, Title: p.Title,
	}, true
}

// renameTab issues the rename; failures are logged by the caller's exit path
// only in aggregate — a rename race is recovered by the next idempotent pass.
func renameTab(sockPath, tabID, label string) {
	_, _ = apiRequest(sockPath, "tab.rename", map[string]string{"tab_id": tabID, "label": label})
}

// computeTabName determines the label a tab should carry, or ok=false when no
// name is computable (no active pane, process-info blip) — in which case the
// tab must be left alone, never fall through to a shell name.
func computeTabName(sockPath string, tab Tab, snap *Snapshot, cfg *TabsConfig) (string, bool) {
	paneID := activePane(tab, snap)
	if paneID == "" {
		return "", false
	}
	// Titles win over the process-derived name (and skip the process-info
	// call). The agent's reported title and the pane's terminal title are
	// the same underlying string in a herdr snapshot.
	var agentKind, title string
	for _, a := range snap.Agents {
		if a.PaneID == paneID {
			agentKind, title = a.Kind, a.Title
			break
		}
	}
	if title == "" {
		for _, p := range snap.Panes {
			if p.PaneID == paneID {
				title = p.Title
				break
			}
		}
	}
	if name, ok := titleTabName(agentKind, title, cfg); ok {
		return name, true
	}
	// If we can't determine a title, only fetch foreground program info if
	// the tab is focused or contains exactly 1 pane, otherwise we can't
	// determine which pane should own the tab's name reliably, and should
	// leave the old name alone to avoid bouncing between panes.
	if !tab.Focused && tab.PaneCount > 1 {
		return "", false
	}
	prog, cmdline, err := paneProgram(sockPath, paneID)
	if err != nil || prog == "" {
		return "", false
	}
	return FormatTabName(prog, cmdline, cfg), true
}

// RenameTabForTitle applies a pane's terminal title to its tab — the daemon's
// targeted path for pane.updated events, which carry the new title in the
// payload. An empty agentKind means a plain pane (shell/program title);
// otherwise the title is an agent session title. An empty title is a clear:
// the tab falls back to the pane's foreground program name — the one case
// here that needs a process-info call. Don't rename a multi-pane tab if we
// don't know the focused pane, to avoid bouncing between panes. The caller
// holds the per-session lock. An opted-out tab is a no-op. retryFull=true
// means a transient error (tab.get or process-info) prevented a rename that
// was due — the caller should schedule a full pass, because the event that
// carried this title will not be resent.
func RenameTabForTitle(sockPath, statePath, tabID, paneID, agentKind, title string, focusKnown bool, cfg *TabsConfig) (retryFull bool, err error) {
	if !cfg.Enabled {
		return false, nil
	}
	if !cfg.TerminalTitles && (!cfg.AgentTitles || agentKind == "") {
		// Not using terminal title as tab name
		return false, nil
	}
	label, paneCount, tabFocused, ok := tabInfo(sockPath, tabID)
	if !ok {
		// The event's dedup entry is already committed and herdr will not
		// re-emit an unchanged title, so the caller must escalate to a full
		// pass, or the rename is lost.
		return true, nil
	}
	if !focusKnown && paneCount > 1 {
		// Can't determine focused pane, don't rename here. A future full
		// pass will take care of it when the tab is focused or the layout's
		// focused pane becomes known.
		return false, nil
	}
	name, titled := titleTabName(agentKind, title, cfg)
	if !titled {
		// Title was explicitly cleared, fall back to the pane's foreground
		// program name except for background multi-pane tabs.
		if !tabFocused && paneCount > 1 {
			return false, nil
		}
		prog, cmdline, err := paneProgram(sockPath, paneID)
		if err != nil || prog == "" {
			return true, nil // process-info blip: escalate
		}
		name = FormatTabName(prog, cmdline, cfg)
		if name == "" && !cfg.HideShell {
			return false, nil
		}
	}
	states := LoadTabStates(statePath)
	if os.Getenv("HWT_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "DEBUG rename tab=%s computed=%q label=%q state=%+v\n", tabID, name, label, states[tabID])
	}
	if !states.Eligible(tabID, label, name, false) {
		return false, SaveTabStates(statePath, states) // Eligible may record an opt-out
	}
	if name != label {
		renameTab(sockPath, tabID, name)
	}
	states[tabID] = TabState{Auto: name, Enabled: true}
	return false, SaveTabStates(statePath, states)
}

// ReconcileTabs walks every tab once, idempotently: compute the desired
// label, check eligibility, rename only when the label actually changes, and
// record ownership. Prunes state for tabs that no longer exist. forceTab
// re-adopts one tab regardless of its opt-out (the reset action).
func ReconcileTabs(sockPath string, snap *Snapshot, cfg *TabsConfig, states TabStates, forceTab string) {
	var seen []string
	for _, tab := range snap.Tabs {
		seen = append(seen, tab.TabID)

		force := forceTab != "" && forceTab == tab.TabID
		// Cheap skip for opted-out tabs: no process-info, no state churn.
		// A cleared label (empty or reverted to the bare tab number) still
		// falls through so Eligible can re-adopt it.
		if st, ok := states[tab.TabID]; ok && !st.Enabled && !isPlaceholder(tab.Label) && !force {
			continue
		}

		name, ok := computeTabName(sockPath, tab, snap, cfg)
		if !ok {
			continue
		}
		// An empty name is only a real label under hide_shell.
		if name == "" && !cfg.HideShell {
			continue
		}
		if !states.Eligible(tab.TabID, tab.Label, name, force) {
			continue
		}
		if name != tab.Label {
			renameTab(sockPath, tab.TabID, name)
			if tab.TabID == snap.FocusedTabID {
				snap.TabLabel = name
			}
		}
		// Ownership is recorded even when no rename was needed.
		states[tab.TabID] = TabState{Auto: name, Enabled: true}
	}
	states.Prune(seen)
}
