package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var pbtxtTitleRegex = regexp.MustCompile(`title:\s*["']?([^"'\r\n]+)["']?`)

// isGenericAgentTitle reports whether a title is just the command line invocation
// (e.g. "agy --continue", "agent", "cursor") rather than an actual conversation title.
func isGenericAgentTitle(title string) bool {
	clean := strings.TrimSpace(strings.ToLower(title))
	switch clean {
	case "", "agy", "agy --continue", "agy -c", "agy --resume", "agy -r",
		"antigravity", "antigravity --continue", "antigravity -c", "antigravity --resume", "antigravity -r",
		"antigravity-cli", "antigravity-cli --continue", "antigravity-cli --resume",
		"agent", "agent code", "agent --continue", "agent -c", "agent --resume", "agent -r",
		"cursor", "cursor-agent", "cursor-cli":
		return true
	}
	if strings.HasPrefix(clean, "agy ") || strings.HasPrefix(clean, "agent ") || strings.HasPrefix(clean, "cursor ") {
		return true
	}
	return false
}

// readAntigravityTitle searches for the active Antigravity session title for cwd.
func readAntigravityTitle(cwd string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	root := os.Getenv("ANTIGRAVITY_DATA_DIR")
	if root == "" {
		root = os.Getenv("ANTIGRAVITY_HOME")
	}
	if root == "" {
		root = filepath.Join(home, ".gemini", "antigravity-cli")
	}

	normCwd := filepath.Clean(cwd)

	// 1. Check annotations/*.pbtxt
	annotationsDir := filepath.Join(root, "annotations")
	entries, err := os.ReadDir(annotationsDir)
	if err == nil {
		type annotated struct {
			path    string
			convID  string
			modTime time.Time
		}
		var list []annotated
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".pbtxt") {
				continue
			}
			full := filepath.Join(annotationsDir, e.Name())
			if info, err := e.Info(); err == nil {
				convID := strings.TrimSuffix(e.Name(), ".pbtxt")
				list = append(list, annotated{path: full, convID: convID, modTime: info.ModTime()})
			}
		}
		// Sort newest first
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				if list[j].modTime.After(list[i].modTime) {
					list[i], list[j] = list[j], list[i]
				}
			}
		}

		// First try to match by conversation's working directory if known
		for _, item := range list {
			if matchesAntigravityCwd(root, item.convID, normCwd) {
				if title := parsePbtxtTitle(item.path); title != "" {
					return title
				}
			}
		}

		// Fallback: newest annotation
		if len(list) > 0 {
			if title := parsePbtxtTitle(list[0].path); title != "" {
				return title
			}
		}
	}

	return ""
}

func parsePbtxtTitle(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	m := pbtxtTitleRegex.FindSubmatch(data)
	if len(m) > 1 {
		return strings.TrimSpace(string(m[1]))
	}
	return ""
}

func matchesAntigravityCwd(root, convID, normCwd string) bool {
	transcriptPath := filepath.Join(root, "brain", convID, ".system_generated", "logs", "transcript.jsonl")
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		return false
	}
	// Cheap check: look for normCwd within transcript bytes
	return strings.Contains(string(data), normCwd)
}

// readCursorTitle searches for the active Cursor session title for cwd.
func readCursorTitle(cwd string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	root := os.Getenv("CURSOR_CONFIG_DIR")
	if root == "" {
		root = filepath.Join(home, ".cursor")
	}
	chatsDir := filepath.Join(root, "chats")

	projects, err := os.ReadDir(chatsDir)
	if err != nil {
		return ""
	}

	normCwd := filepath.Clean(cwd)
	var latestTitle string
	var latestTime int64

	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		projDir := filepath.Join(chatsDir, p.Name())
		sessions, err := os.ReadDir(projDir)
		if err != nil {
			continue
		}
		for _, s := range sessions {
			if !s.IsDir() {
				continue
			}
			metaPath := filepath.Join(projDir, s.Name(), "meta.json")
			data, err := os.ReadFile(metaPath)
			if err != nil {
				continue
			}
			var meta struct {
				Title       string `json:"title"`
				CWD         string `json:"cwd"`
				UpdatedAtMs int64  `json:"updatedAtMs"`
			}
			if err := json.Unmarshal(data, &meta); err == nil {
				if filepath.Clean(meta.CWD) == normCwd && meta.Title != "" && meta.UpdatedAtMs > latestTime {
					latestTime = meta.UpdatedAtMs
					latestTitle = meta.Title
				}
			}
		}
	}

	return latestTitle
}

// readAgentTitle queries disk metadata for known agents when terminal title is absent or generic.
func readAgentTitle(agentKind, cwd string) string {
	switch strings.ToLower(agentKind) {
	case "agy", "antigravity", "antigravity-cli":
		return readAntigravityTitle(cwd)
	case "agent", "cursor", "cursor-agent", "cursor-cli":
		return readCursorTitle(cwd)
	}
	return ""
}
