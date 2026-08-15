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

// tabLabel fetches a tab's current label. ok is false when the call fails or
// returns no tab — a failed get must NOT look like an empty label, which
// would read as a placeholder and clobber a hand-picked name.
func tabLabel(sockPath, tabID string) (string, bool) {
	result, err := apiRequest(sockPath, "tab.get", map[string]string{"tab_id": tabID})
	if err != nil {
		return "", false
	}
	var payload struct {
		Tab *struct {
			Label string `json:"label"`
		} `json:"tab"`
	}
	if err := json.Unmarshal(result, &payload); err != nil || payload.Tab == nil {
		return "", false
	}
	return payload.Tab.Label, true
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
	if cfg.AgentTitles {
		for _, a := range snap.Agents {
			if a.PaneID == paneID && a.Title != "" {
				return FormatAgentTitle(a.Kind, a.Title, cfg), true
			}
		}
	}
	// A pane's own terminal title wins over the process-derived name (and
	// skips the process-info subprocess) when terminal_titles=true. An
	// agent pane reach here only with agent_titles=false (or a blank agent
	// title, which is this same string), and its title counts like any
	// other pane's.
	if cfg.TerminalTitles {
		for _, p := range snap.Panes {
			if p.PaneID == paneID && p.Title != "" {
				return FormatTerminalTitle(p.Title, cfg), true
			}
		}
	}
	prog, cmdline, err := paneProgram(sockPath, paneID)
	if err != nil || prog == "" {
		return "", false
	}
	return FormatTabName(prog, cmdline, cfg), true
}

// RenameTabForTitle applies a pane's terminal title to its tab — the daemon's
// targeted path for pane.updated events, which carry the new title in the
// payload, so no process-info subprocess is needed. An empty agentKind means
// a plain pane (shell/program title); otherwise the title is an agent session
// title. The caller holds the per-session lock. An empty title or an
// opted-out tab is a no-op.
func RenameTabForTitle(sockPath, statePath, tabID, agentKind, title string, cfg *TabsConfig) error {
	if !cfg.Enabled || title == "" {
		return nil
	}
	var name string
	switch {
	case agentKind != "" && cfg.AgentTitles:
		name = FormatAgentTitle(agentKind, title, cfg)
	case cfg.TerminalTitles:
		name = FormatTerminalTitle(title, cfg)
	default:
		return nil
	}
	label, ok := tabLabel(sockPath, tabID)
	if !ok {
		return nil
	}
	states := LoadTabStates(statePath)
	if os.Getenv("HWT_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "DEBUG rename tab=%s computed=%q label=%q state=%+v\n", tabID, name, label, states[tabID])
	}
	if !states.Eligible(tabID, label, name, false) {
		return SaveTabStates(statePath, states) // Eligible may record an opt-out
	}
	if name != label {
		renameTab(sockPath, tabID, name)
	}
	states[tabID] = TabState{Auto: name, Enabled: true}
	return SaveTabStates(statePath, states)
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
