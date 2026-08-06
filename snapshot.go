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

// Agent is the slice of a herdr agent the title cares about.
type Agent struct {
	Status      string
	WorkspaceID string
}

// Snapshot is the slice of `herdr api snapshot` the title cares about.
type Snapshot struct {
	FocusedWorkspaceID string
	FocusedTabID       string
	SpaceLabel         string
	TabLabel           string
	Agents             []Agent
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
					TabID string `json:"tab_id"`
					Label string `json:"label"`
				} `json:"tabs"`
				Agents []struct {
					AgentStatus string `json:"agent_status"`
					WorkspaceID string `json:"workspace_id"`
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
			snap.SpaceLabel = w.Label
		}
	}
	for _, t := range raw.Tabs {
		if t.TabID == raw.FocusedTabID {
			snap.TabLabel = t.Label
		}
	}
	for _, a := range raw.Agents {
		snap.Agents = append(snap.Agents, Agent{Status: a.AgentStatus, WorkspaceID: a.WorkspaceID})
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
