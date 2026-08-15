package main

import (
	"encoding/json"
	"fmt"
)

// Agent is the slice of a herdr agent the plugin cares about.
type Agent struct {
	Status      string
	WorkspaceID string
	PaneID      string
	Kind        string // herdr's detected agent kind, e.g. "claude"
	Title       string // terminal_title_stripped: the agent's session title
}

// Tab is the slice of a herdr tab the plugin cares about.
type Tab struct {
	TabID       string
	WorkspaceID string
	Label       string
	PaneCount   int
	Focused     bool
}

// Pane is the slice of a herdr pane the plugin cares about.
type Pane struct {
	PaneID  string
	TabID   string
	Agent   string // detected agent kind, "" for a plain pane
	Focused bool
	Title   string // terminal_title_stripped: the pane's terminal title
}

// Snapshot is the slice of `herdr api snapshot` the plugin cares about.
type Snapshot struct {
	FocusedWorkspaceID string
	FocusedTabID       string
	WorkspaceLabel     string
	TabLabel           string
	Agents             []Agent
	Tabs               []Tab
	Panes              []Pane
	TabFocus           map[string]string // tab_id -> focused_pane_id
}

// decodeSnapshot parses a session.snapshot RESULT payload: {"snapshot":{...}}.
func decodeSnapshot(data []byte) (*Snapshot, error) {
	var payload struct {
		Snapshot *struct {
			FocusedWorkspaceID string `json:"focused_workspace_id"`
			FocusedTabID       string `json:"focused_tab_id"`
			Workspaces         []struct {
				WorkspaceID string `json:"workspace_id"`
				Label       string `json:"label"`
			} `json:"workspaces"`
			Tabs []struct {
				TabID       string `json:"tab_id"`
				WorkspaceID string `json:"workspace_id"`
				Label       string `json:"label"`
				PaneCount   int    `json:"pane_count"`
				Focused     bool   `json:"focused"`
			} `json:"tabs"`
			Panes []struct {
				PaneID        string `json:"pane_id"`
				TabID         string `json:"tab_id"`
				Agent         string `json:"agent"`
				Focused       bool   `json:"focused"`
				TitleStripped string `json:"terminal_title_stripped"`
			} `json:"panes"`
			Agents []struct {
				AgentStatus   string `json:"agent_status"`
				WorkspaceID   string `json:"workspace_id"`
				PaneID        string `json:"pane_id"`
				Agent         string `json:"agent"`
				TitleStripped string `json:"terminal_title_stripped"`
			} `json:"agents"`
			Layouts []struct {
				TabID         string `json:"tab_id"`
				FocusedPaneID string `json:"focused_pane_id"`
			} `json:"layouts"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	raw := payload.Snapshot
	if raw == nil {
		return nil, fmt.Errorf("decode snapshot: no snapshot payload in response")
	}

	snap := &Snapshot{
		FocusedWorkspaceID: raw.FocusedWorkspaceID,
		FocusedTabID:       raw.FocusedTabID,
		TabFocus:           map[string]string{},
	}
	for _, l := range raw.Layouts {
		if l.FocusedPaneID != "" {
			snap.TabFocus[l.TabID] = l.FocusedPaneID
		}
	}
	for _, w := range raw.Workspaces {
		if w.WorkspaceID == raw.FocusedWorkspaceID {
			snap.WorkspaceLabel = w.Label
		}
	}
	for _, t := range raw.Tabs {
		if t.TabID == raw.FocusedTabID {
			snap.TabLabel = t.Label
		}
		snap.Tabs = append(snap.Tabs, Tab{
			TabID: t.TabID, WorkspaceID: t.WorkspaceID, Label: t.Label,
			PaneCount: t.PaneCount, Focused: t.Focused,
		})
	}
	for _, p := range raw.Panes {
		snap.Panes = append(snap.Panes, Pane{
			PaneID: p.PaneID, TabID: p.TabID, Agent: p.Agent,
			Focused: p.Focused, Title: p.TitleStripped,
		})
	}
	for _, a := range raw.Agents {
		snap.Agents = append(snap.Agents, Agent{
			Status: a.AgentStatus, WorkspaceID: a.WorkspaceID,
			PaneID: a.PaneID, Kind: a.Agent, Title: a.TitleStripped,
		})
	}
	return snap, nil
}

// FetchSnapshot asks the session socket for a full state snapshot.
func FetchSnapshot(sockPath string) (*Snapshot, error) {
	result, err := apiRequest(sockPath, "session.snapshot", nil)
	if err != nil {
		return nil, err
	}
	return decodeSnapshot(result)
}
