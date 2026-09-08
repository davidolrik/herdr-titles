package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsGenericAgentTitle(t *testing.T) {
	tests := []struct {
		title string
		want  bool
	}{
		{"", true},
		{"agy", true},
		{"agy --continue", true},
		{"agy -c", true},
		{"agy --resume", true},
		{"agy -r", true},
		{"antigravity", true},
		{"antigravity-cli", true},
		{"agent", true},
		{"agent code", true},
		{"agent --continue", true},
		{"cursor", true},
		{"cursor-agent", true},
		{"cursor-cli", true},
		{"agy -d something", true},
		{"agent --flag", true},
		{"cursor ./src", true},
		{"cursor /tmp/proj", true},
		{"Fix login bug", false},
		{"Refactor database queries", false},
		{"AGY support", false},
		{"Cursor chat", false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("title=%q", tt.title), func(t *testing.T) {
			if got := isGenericAgentTitle(tt.title); got != tt.want {
				t.Errorf("isGenericAgentTitle(%q) = %v, want %v", tt.title, got, tt.want)
			}
		})
	}
}

func TestParsePbtxtTitle(t *testing.T) {
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.pbtxt")
	content := []byte("session_id: \"123\"\ntitle: \"Build New Feature\"\nstatus: ACTIVE\n")
	if err := os.WriteFile(testFile, content, 0o644); err != nil {
		t.Fatal(err)
	}

	got := parsePbtxtTitle(testFile)
	if got != "Build New Feature" {
		t.Errorf("parsePbtxtTitle() = %q, want %q", got, "Build New Feature")
	}

	// Missing file returns empty string
	if got := parsePbtxtTitle(filepath.Join(tmpDir, "missing.pbtxt")); got != "" {
		t.Errorf("parsePbtxtTitle(missing) = %q, want empty", got)
	}
}

func TestReadAntigravityTitle(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ANTIGRAVITY_DATA_DIR", tmpDir)

	cwd := "/test/project/dir"

	// Create annotations directory
	annotationsDir := filepath.Join(tmpDir, "annotations")
	if err := os.MkdirAll(annotationsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Conv 1 (old)
	conv1ID := "conv-1"
	pbtxt1 := filepath.Join(annotationsDir, conv1ID+".pbtxt")
	if err := os.WriteFile(pbtxt1, []byte("title: \"Old Session\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Conv 2 (newer, matching cwd)
	conv2ID := "conv-2"
	pbtxt2 := filepath.Join(annotationsDir, conv2ID+".pbtxt")
	if err := os.WriteFile(pbtxt2, []byte("title: \"Feature Work\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Make conv2 newer
	now := time.Now().Add(time.Hour)
	_ = os.Chtimes(pbtxt2, now, now)

	// Create transcript for conv2 with matching cwd
	transcriptDir := filepath.Join(tmpDir, "brain", conv2ID, ".system_generated", "logs")
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(transcriptDir, "transcript.jsonl")
	transcriptContent := fmt.Sprintf(`{"step_index":0,"content":"working in %s"}`+"\n", cwd)
	if err := os.WriteFile(transcriptPath, []byte(transcriptContent), 0o644); err != nil {
		t.Fatal(err)
	}

	title := readAntigravityTitle(cwd)
	if title != "Feature Work" {
		t.Errorf("readAntigravityTitle(%q) = %q, want %q", cwd, title, "Feature Work")
	}

	// For a different cwd with no transcript match, falls back to newest annotation
	fallbackTitle := readAntigravityTitle("/other/cwd")
	if fallbackTitle != "Feature Work" {
		t.Errorf("readAntigravityTitle(/other/cwd) = %q, want %q", fallbackTitle, "Feature Work")
	}
}

func TestReadCursorTitle(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("CURSOR_CONFIG_DIR", tmpDir)

	cwd := "/test/cursor/project"

	projDir := filepath.Join(tmpDir, "chats", "proj1")
	sessionDir := filepath.Join(projDir, "sess1")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	meta := map[string]any{
		"title":       "Cursor Refactoring",
		"cwd":         cwd,
		"updatedAtMs": time.Now().UnixMilli(),
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}

	metaPath := filepath.Join(sessionDir, "meta.json")
	if err := os.WriteFile(metaPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	title := readCursorTitle(cwd)
	if title != "Cursor Refactoring" {
		t.Errorf("readCursorTitle(%q) = %q, want %q", cwd, title, "Cursor Refactoring")
	}

	// Mismatched cwd
	if title := readCursorTitle("/unrelated/path"); title != "" {
		t.Errorf("readCursorTitle(unrelated) = %q, want empty", title)
	}
}

func TestReadAgentTitle(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ANTIGRAVITY_DATA_DIR", tmpDir)
	t.Setenv("CURSOR_CONFIG_DIR", tmpDir)

	cwd := "/test/work"

	// Setup Antigravity
	annotationsDir := filepath.Join(tmpDir, "annotations")
	_ = os.MkdirAll(annotationsDir, 0o755)
	_ = os.WriteFile(filepath.Join(annotationsDir, "agy1.pbtxt"), []byte("title: \"AGY Task\"\n"), 0o644)

	if got := readAgentTitle("agy", cwd); got != "AGY Task" {
		t.Errorf("readAgentTitle(agy) = %q, want %q", got, "AGY Task")
	}

	// Unknown agent kind
	if got := readAgentTitle("unknown-agent", cwd); got != "" {
		t.Errorf("readAgentTitle(unknown) = %q, want empty", got)
	}
}
