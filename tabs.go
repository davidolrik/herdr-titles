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
			PaneID        string `json:"pane_id"`
			TabID         string `json:"tab_id"`
			Agent         string `json:"agent"`
			Label         string `json:"label"`
			Focused       bool   `json:"focused"`
			Title         string `json:"terminal_title_stripped"`
			CWD           string `json:"cwd"`
			ForegroundCWD string `json:"foreground_cwd"`
		} `json:"pane"`
	}
	if err := json.Unmarshal(result, &payload); err != nil || payload.Pane == nil {
		return Pane{}, false
	}
	p := payload.Pane
	title := p.Title
	if title == "" {
		title = p.Label
	}
	cwd := p.ForegroundCWD
	if cwd == "" {
		cwd = p.CWD
	}
	return Pane{
		PaneID: p.PaneID, TabID: p.TabID, Agent: p.Agent,
		Focused: p.Focused, Title: title, CWD: cwd,
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
func computeTabName(sockPath string, tab Tab, snap *Snapshot, cfg *TabsConfig, dismissedTitle string) (string, bool) {
	paneID := activePane(tab, snap)
	if paneID == "" {
		return "", false
	}
	// Titles win over the process-derived name (and skip the process-info
	// call). The agent's reported title and the pane's terminal title are
	// the same underlying string in a herdr snapshot.
	var agentKind, title, cwd string
	for _, a := range snap.Agents {
		if a.PaneID == paneID {
			agentKind, title, cwd = a.Kind, a.Title, a.CWD
			break
		}
	}
	for _, p := range snap.Panes {
		if p.PaneID == paneID {
			if agentKind == "" {
				agentKind = p.Agent
			}
			if title == "" {
				title = p.Title
			}
			if cwd == "" {
				cwd = p.CWD
			}
			break
		}
	}
	if (title == "" || isGenericAgentTitle(title)) && cfg.AgentTitles {
		if agentKind == "" && (tab.Focused || tab.PaneCount == 1) {
			if prog, _, err := paneProgram(sockPath, paneID); err == nil && isAgentProgram(prog) {
				agentKind = prog
			}
		}
		if agentKind != "" {
			if agentTitle := readAgentTitle(agentKind, cwd); agentTitle != "" {
				title = agentTitle
			}
		}
	}
	// A title the user dismissed with reset counts as no title at all.
	if dismissedTitle != "" && title == dismissedTitle {
		title = ""
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
		// Could not determine the program: leave the tab name alone.
		return "", false
	}
	return FormatTabName(prog, cmdline, cfg), true
}

// paneTitle returns the title a pane carries in the snapshot: the agent's
// reported title if it hosts one, else its terminal title, "" if neither.
func paneTitle(paneID string, snap *Snapshot) string {
	if paneID == "" {
		return ""
	}
	for _, a := range snap.Agents {
		if a.PaneID == paneID && a.Title != "" {
			return a.Title
		}
	}
	for _, p := range snap.Panes {
		if p.PaneID == paneID {
			return p.Title
		}
	}
	return ""
}

// RenameTabForTitle is the targeted rename path for pane.updated events.
// It computes the title-derived tab name without fetching a full snapshot,
// using only the tab_id, pane_id, and new title from the event. It does not
// handle structural events (pane closed, tab moved), which still go through
// full passes.
//
// Returns retryFull=true when the rename could not complete and a full pass
// was due — the caller should schedule a full pass, because the event that
// carried this title will not be resent.
func RenameTabForTitle(sockPath, statePath, tabID, paneID, agentKind, title string, focusKnown bool, cfg *TabsConfig) (retryFull bool, err error) {
	if !cfg.Enabled {
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
	if (title == "" || isGenericAgentTitle(title)) && cfg.AgentTitles {
		if agentKind == "" && (tabFocused || paneCount == 1) {
			if prog, _, err := paneProgram(sockPath, paneID); err == nil && isAgentProgram(prog) {
				agentKind = prog
			}
		}
		if agentKind != "" {
			if pane, ok := paneInfo(sockPath, paneID); ok {
				if agentTitle := readAgentTitle(agentKind, pane.CWD); agentTitle != "" {
					title = agentTitle
				}
			}
		}
	}
	if !cfg.TerminalTitles && (!cfg.AgentTitles || agentKind == "") {
		// Not using terminal title as tab name
		return false, nil
	}
	states := LoadTabStates(statePath)
	dismissed := states[tabID].DismissedTitle
	if dismissed != "" {
		if title == dismissed {
			// The user rejected this exact title with reset: act as a clear.
			title = ""
		} else {
			dismissed = "" // a different title is fresh information
		}
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
	if os.Getenv("HWT_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "DEBUG rename tab=%s computed=%q label=%q state=%+v\n", tabID, name, label, states[tabID])
	}
	if !states.Eligible(tabID, label, name, false) {
		return false, SaveTabStates(statePath, states) // Eligible may record an opt-out
	}
	if name != label {
		renameTab(sockPath, tabID, name)
	}
	states[tabID] = TabState{Auto: name, Enabled: true, DismissedTitle: dismissed}
	return false, SaveTabStates(statePath, states)
}

// renameFromEvent is the daemon's rename op for a pane.updated event. The
// event is a nudge, not the truth: herdr hands the stream out at a bounded
// rate (one event per type per tick), and a fresh subscription first replays
// buffered history — so the payload's title can be seconds behind, or minutes
// after a daemon (re)start, and applying it as-is renamed tabs to titles that
// were long gone. The pane's CURRENT title and agent are fetched and applied
// instead; a stale event thus lands on the right name immediately rather than
// after the queue drains. A pane that no longer exists, or that has moved to
// another tab, is left to the full pass its own event schedules. Same
// contract as RenameTabForTitle otherwise; the caller holds the lock.
func renameFromEvent(sockPath, statePath string, p paneEvent, cfg *TabsConfig) (retryFull bool, err error) {
	pane, ok := paneInfo(sockPath, p.PaneID)
	if !ok || (pane.TabID != "" && pane.TabID != p.TabID) {
		return false, nil
	}
	return RenameTabForTitle(sockPath, statePath, p.TabID, p.PaneID, pane.Agent, pane.Title, p.FocusKnown, cfg)
}

// ReconcileTabs walks every tab once, idempotently: compute the desired
// label, check eligibility, rename only when the label actually changes, and
// record ownership. Prunes state for tabs that no longer exist. forceTab
// re-adopts one tab regardless of its opt-out (the reset action).
func ReconcileTabs(sockPath string, snap *Snapshot, cfg *TabsConfig, states TabStates, forceTab string) {
	var seen []string
	for _, tab := range snap.Tabs {
		seen = append(seen, tab.TabID)
		reconcileTab(sockPath, tab, snap, cfg, states, forceTab != "" && forceTab == tab.TabID)
	}
	states.Prune(seen)
}

// ReconcileTabByID is a full pass narrowed to one tab — the shell hook's
// path under terminal_titles. The daemon's pane.updated stream only speaks
// for panes that publish a title; a pane that never does (a shell without
// the integration, HERDR_TITLES_NO_TITLE=1, a command whose first word is a
// function or an assignment) would otherwise change its tab only on some
// unrelated event's full pass, and a command that ended in a background tab
// would keep the tab until the tab was next focused. It computes exactly
// what the next full pass would (titles win, dismissals and multi-pane focus
// honored, process-info only for an untitled pane), so it can never disagree
// with the daemon. A tab the snapshot no longer knows is a no-op. The caller
// holds the per-session lock.
func ReconcileTabByID(sockPath, statePath, tabID string, cfg *TabsConfig) error {
	snap, err := FetchSnapshot(sockPath)
	if err != nil {
		return err
	}
	for _, tab := range snap.Tabs {
		if tab.TabID != tabID {
			continue
		}
		states := LoadTabStates(statePath)
		reconcileTab(sockPath, tab, snap, cfg, states, false)
		return SaveTabStates(statePath, states)
	}
	return nil
}

// reconcileTab is one tab's share of a pass: compute the desired label,
// check eligibility, rename only when the label actually changes, and record
// ownership. force re-adopts the tab regardless of its opt-out (the reset
// action).
func reconcileTab(sockPath string, tab Tab, snap *Snapshot, cfg *TabsConfig, states TabStates, force bool) {
	// Cheap skip for opted-out tabs: no process-info, no state churn.
	// A cleared label (empty or reverted to the bare tab number) still
	// falls through so Eligible can re-adopt it.
	if st, ok := states[tab.TabID]; ok && !st.Enabled && !isPlaceholder(tab.Label) && !force {
		return
	}

	// A reset rejects whatever title the pane carries right now: it is
	// remembered so the tab is named by its program until the pane emits
	// a different title. An existing dismissal stays only while the pane
	// still shows that exact title.
	dismissed := states[tab.TabID].DismissedTitle
	current := paneTitle(activePane(tab, snap), snap)
	switch {
	case force, isClearGesture(tab.Label):
		// The reset action, or the whitespace rename the README teaches
		// (herdr's rename UI accepts spaces but not an empty name), both
		// reject the current title. Herdr's own reversion to the bare tab
		// number is not a user gesture and keeps the title.
		dismissed = current
	case dismissed != "" && current != dismissed:
		dismissed = ""
	}

	name, ok := computeTabName(sockPath, tab, snap, cfg, dismissed)
	if !ok {
		return
	}
	// An empty name is only a real label under hide_shell.
	if name == "" && !cfg.HideShell {
		return
	}
	if !states.Eligible(tab.TabID, tab.Label, name, force) {
		return
	}
	if name != tab.Label {
		renameTab(sockPath, tab.TabID, name)
		if tab.TabID == snap.FocusedTabID {
			snap.TabLabel = name
		}
	}
	// Ownership is recorded even when no rename was needed.
	states[tab.TabID] = TabState{Auto: name, Enabled: true, DismissedTitle: dismissed}
}
