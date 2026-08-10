package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

const herdrTimeout = 10 * time.Second

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
	Focused bool
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
}

func decodeSnapshot(data []byte) (*Snapshot, error) {
	var payload struct {
		Result struct {
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
					PaneID  string `json:"pane_id"`
					TabID   string `json:"tab_id"`
					Focused bool   `json:"focused"`
				} `json:"panes"`
				Agents []struct {
					AgentStatus   string `json:"agent_status"`
					WorkspaceID   string `json:"workspace_id"`
					PaneID        string `json:"pane_id"`
					Agent         string `json:"agent"`
					TitleStripped string `json:"terminal_title_stripped"`
				} `json:"agents"`
			} `json:"snapshot"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	raw := payload.Result.Snapshot
	if raw == nil {
		return nil, fmt.Errorf("decode snapshot: no snapshot payload in response")
	}

	snap := &Snapshot{
		FocusedWorkspaceID: raw.FocusedWorkspaceID,
		FocusedTabID:       raw.FocusedTabID,
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
		snap.Panes = append(snap.Panes, Pane{PaneID: p.PaneID, TabID: p.TabID, Focused: p.Focused})
	}
	for _, a := range raw.Agents {
		snap.Agents = append(snap.Agents, Agent{
			Status: a.AgentStatus, WorkspaceID: a.WorkspaceID,
			PaneID: a.PaneID, Kind: a.Agent, Title: a.TitleStripped,
		})
	}
	return snap, nil
}

// FetchSnapshot runs `<herdrBin> api snapshot` and decodes the result.
func FetchSnapshot(herdrBin string) (*Snapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), herdrTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, herdrBin, "api", "snapshot")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s api snapshot: %w", herdrBin, err)
	}
	return decodeSnapshot(stdout.Bytes())
}
