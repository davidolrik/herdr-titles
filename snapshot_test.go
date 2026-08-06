package main

import (
	"os"
	"testing"
)

func TestDecodeSnapshotFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := decodeSnapshot(data)
	if err != nil {
		t.Fatalf("decodeSnapshot: %v", err)
	}
	if snap.FocusedWorkspaceID != "wE" {
		t.Errorf("FocusedWorkspaceID = %q, want wE", snap.FocusedWorkspaceID)
	}
	if snap.SpaceLabel != "herdr-overseer" {
		t.Errorf("SpaceLabel = %q, want herdr-overseer", snap.SpaceLabel)
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
	}
	if working != 1 {
		t.Errorf("working agents = %d, want 1", working)
	}
}

func TestDecodeSnapshotInvalid(t *testing.T) {
	if _, err := decodeSnapshot([]byte(`{"result":{}}`)); err == nil {
		t.Fatal("expected error for snapshot without payload, got nil")
	}
	if _, err := decodeSnapshot([]byte(`not json`)); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
