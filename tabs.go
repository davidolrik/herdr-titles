package main

// Tab-name reconciliation, ported (minus jump-key numbering) from
// automatic-rename.sh of qu8n/herdr-automatic-rename (MIT, (c) Quan Nguyen),
// plus the agent-title mode: a pane hosting a recognized agent can name its
// tab after the agent's session title instead of the process name.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// activePane resolves which pane a tab is named after: a single-pane tab by
// its sole pane whether or not it is focused; a focused multi-pane tab by the
// globally focused pane; a background multi-pane tab by none — no name is
// computable and the tab keeps its current label. A split therefore flips a
// tab between nameable and un-nameable, which is why pane lifecycle events
// are subscribed.
func activePane(tab Tab, panes []Pane) string {
	var tabPanes []Pane
	for _, p := range panes {
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
	if tab.Focused {
		for _, p := range panes {
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
func paneProgram(herdrBin, paneID string) (prog, cmdline string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), herdrTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, herdrBin, "pane", "process-info", "--pane", paneID)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("pane process-info %s: %w", paneID, err)
	}

	var payload struct {
		Result struct {
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
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return "", "", fmt.Errorf("pane process-info %s: %w", paneID, err)
	}

	info := payload.Result.ProcessInfo
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
		cmdline = p.Cmdline
		if cmdline == "" {
			cmdline = strings.Join(p.Argv, " ")
		}
		return prog, cmdline, nil
	}
	return "", "", fmt.Errorf("pane process-info %s: no group leader", paneID)
}

// tabLabel fetches a tab's current label. ok is false when the call fails or
// returns no tab — a failed get must NOT look like an empty label, which
// would read as a placeholder and clobber a hand-picked name.
func tabLabel(herdrBin, tabID string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), herdrTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, herdrBin, "tab", "get", tabID)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", false
	}
	var payload struct {
		Result struct {
			Tab *struct {
				Label string `json:"label"`
			} `json:"tab"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil || payload.Result.Tab == nil {
		return "", false
	}
	return payload.Result.Tab.Label, true
}

// renameTab issues the rename; failures are logged by the caller's exit path
// only in aggregate — a rename race is recovered by the next idempotent pass.
func renameTab(herdrBin, tabID, label string) {
	ctx, cancel := context.WithTimeout(context.Background(), herdrTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, herdrBin, "tab", "rename", tabID, label).Run()
}

// computeTabName determines the label a tab should carry, or ok=false when no
// name is computable (no active pane, process-info blip) — in which case the
// tab must be left alone, never fall through to a shell name.
func computeTabName(herdrBin string, tab Tab, snap *Snapshot, cfg *TabsConfig) (string, bool) {
	paneID := activePane(tab, snap.Panes)
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
	prog, cmdline, err := paneProgram(herdrBin, paneID)
	if err != nil || prog == "" {
		return "", false
	}
	return FormatTabName(prog, cmdline, cfg), true
}

// RenameTabForAgentTitle applies an agent's session title to its tab — the
// daemon's targeted path for pane.updated events, which carry the new title
// in the payload, so no process-info subprocess is needed. The caller holds
// the per-session lock. An empty title or an opted-out tab is a no-op.
func RenameTabForAgentTitle(herdrBin, statePath, tabID, agentKind, title string, cfg *TabsConfig) error {
	if !cfg.Enabled || !cfg.AgentTitles || title == "" {
		return nil
	}
	name := FormatAgentTitle(agentKind, title, cfg)
	label, ok := tabLabel(herdrBin, tabID)
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
		renameTab(herdrBin, tabID, name)
	}
	states[tabID] = TabState{Auto: name, Enabled: true}
	return SaveTabStates(statePath, states)
}

// ReconcileTabs walks every tab once, idempotently: compute the desired
// label, check eligibility, rename only when the label actually changes, and
// record ownership. Prunes state for tabs that no longer exist. forceTab
// re-adopts one tab regardless of its opt-out (the reset action).
func ReconcileTabs(herdrBin string, snap *Snapshot, cfg *TabsConfig, states TabStates, forceTab string) {
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

		name, ok := computeTabName(herdrBin, tab, snap, cfg)
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
			renameTab(herdrBin, tab.TabID, name)
			if tab.TabID == snap.FocusedTabID {
				snap.TabLabel = name
			}
		}
		// Ownership is recorded even when no rename was needed.
		states[tab.TabID] = TabState{Auto: name, Enabled: true}
	}
	states.Prune(seen)
}
