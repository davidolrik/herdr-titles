package main

import (
	"testing"
)

func TestDecodeSnapshotFixture(t *testing.T) {
	snap, err := decodeSnapshot(fixtureSnapshotResult(t))
	if err != nil {
		t.Fatalf("decodeSnapshot: %v", err)
	}
	if snap.FocusedWorkspaceID != "wE" {
		t.Errorf("FocusedWorkspaceID = %q, want wE", snap.FocusedWorkspaceID)
	}
	if snap.WorkspaceLabel != "herdr-overseer" {
		t.Errorf("WorkspaceLabel = %q, want herdr-overseer", snap.WorkspaceLabel)
	}
	if snap.TabLabel != "1" {
		t.Errorf("TabLabel = %q, want 1", snap.TabLabel)
	}
	if len(snap.Agents) != 7 {
		t.Fatalf("Agents = %d, want 7", len(snap.Agents))
	}
	working := 0
	for _, a := range snap.Agents {
		if a.Status == "working" {
			working++
		}
		if a.PaneID == "" || a.Kind == "" {
			t.Errorf("agent missing pane/kind: %+v", a)
		}
	}
	if working != 1 {
		t.Errorf("working agents = %d, want 1", working)
	}
	if len(snap.Tabs) == 0 || len(snap.Panes) == 0 {
		t.Fatalf("tabs/panes not decoded: %d tabs, %d panes", len(snap.Tabs), len(snap.Panes))
	}
	for _, tab := range snap.Tabs {
		if tab.TabID == "" || tab.PaneCount == 0 {
			t.Errorf("tab missing fields: %+v", tab)
		}
	}
	titled := 0
	for _, a := range snap.Agents {
		if a.Title != "" {
			titled++
		}
	}
	if titled == 0 {
		t.Error("no agent carried a session title")
	}
	paneTitled := 0
	for _, p := range snap.Panes {
		if p.Title != "" {
			paneTitled++
		}
	}
	if paneTitled == 0 {
		t.Error("no pane carried a terminal title")
	}
	if len(snap.TabFocus) != len(snap.Tabs) {
		t.Errorf("TabFocus has %d entries, want one per tab (%d)", len(snap.TabFocus), len(snap.Tabs))
	}
	for tabID, paneID := range snap.TabFocus {
		if tabID == "" || paneID == "" {
			t.Errorf("TabFocus entry missing ids: %q -> %q", tabID, paneID)
		}
	}
}

func TestDecodeSnapshotInvalid(t *testing.T) {
	if _, err := decodeSnapshot([]byte(`{}`)); err == nil {
		t.Fatal("expected error for snapshot without payload, got nil")
	}
	if _, err := decodeSnapshot([]byte(`not json`)); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
