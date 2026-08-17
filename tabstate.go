package main

// Per-tab naming state. Herdr has no per-tab metadata and no auto/manual
// flag, so which tabs the plugin owns is tracked here: the last base name we
// set per tab and whether auto-naming is still enabled for it. Kept in one
// JSON file PER SESSION (tab ids like "w1:t1" repeat across herdr sessions).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// TabState records ownership of one tab's label.
type TabState struct {
	// Auto is the last base name this plugin set; "" is a real value (a
	// hide_shell tab is owned with an empty label).
	Auto string `json:"auto"`
	// Enabled is false when a manual rename opted the tab out.
	Enabled bool `json:"enabled"`
	// DismissedTitle is a pane terminal title the user rejected with the
	// reset action. The plugin cannot clear a title (it is terminal state
	// with no herdr API), so it remembers the rejected one and names the tab
	// by its program until the pane emits a different title, which clears
	// this. Omitted from JSON when empty.
	DismissedTitle string `json:"dismissed_title,omitempty"`
}

// TabStates maps tab_id to its naming state.
type TabStates map[string]TabState

// isClearGesture reports whether a label is the user's own "clear this"
// rename: whitespace only. Herdr's rename UI rejects an empty submission but
// accepts spaces, so this is the one clear a user can actually type, and —
// unlike the bare tab number herdr reverts to by itself — it is unambiguously
// a user gesture.
func isClearGesture(label string) bool {
	return label != "" && strings.TrimSpace(label) == ""
}

// isPlaceholder reports whether a label counts as "no name": empty, herdr's
// own all-digit tab number, or whitespace only — herdr's rename UI rejects an
// empty submission but accepts spaces, so whitespace is the clear gesture a
// user can actually type there.
func isPlaceholder(label string) bool {
	label = strings.TrimSpace(label)
	if label == "" {
		return true
	}
	for _, r := range label {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Eligible reports whether the tab may be auto-named, updating the state to
// opt a hand-named tab out as a side effect. label is the tab's current
// label; computed is the name the plugin would set (used to self-adopt tabs
// that already carry exactly the name we would give them, e.g. after
// migrating from another naming plugin); force re-adopts regardless (the
// reset action).
func (s TabStates) Eligible(tabID, label, computed string, force bool) bool {
	if force {
		return true
	}
	st, seen := s[tabID]
	switch {
	case !seen:
		if isPlaceholder(label) || label == computed {
			return true
		}
		s[tabID] = TabState{Enabled: false} // hand-named: opt out
		return false
	case !st.Enabled:
		// Only a cleared custom name re-adopts. Herdr's rename UI refuses an
		// empty submission and reverts a dropped custom name to the bare tab
		// number, so both placeholder forms count as "cleared".
		return isPlaceholder(label)
	default: // we own it
		if label == st.Auto || label == "" {
			return true
		}
		if isClearGesture(label) {
			// The whitespace rename is the documented "hand it back" gesture;
			// on an owned tab that just means "name it again now", not a
			// manual rename to opt out of.
			return true
		}
		if st.Auto == "" && isPlaceholder(label) {
			// hide_shell tab: herdr handed its own number back
			return true
		}
		s[tabID] = TabState{Enabled: false} // user renamed: opt out
		return false
	}
}

// Prune drops state for tabs that no longer exist.
func (s TabStates) Prune(seenTabs []string) {
	for tabID := range s {
		if !slices.Contains(seenTabs, tabID) {
			delete(s, tabID)
		}
	}
}

// LoadTabStates reads the state file; a missing or unreadable file is an
// empty, usable map.
func LoadTabStates(path string) TabStates {
	states := TabStates{}
	data, err := os.ReadFile(path)
	if err != nil {
		return states
	}
	_ = json.Unmarshal(data, &states)
	return states
}

// SaveTabStates writes atomically (temp + rename) so a crashed run never
// leaves a torn file behind.
func SaveTabStates(path string, s TabStates) error {
	data, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tabstate.*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// tabStatePath names the per-session state file inside stateDir.
func tabStatePath(stateDir, session string) string {
	return filepath.Join(stateDir, "tabstate."+strings.ReplaceAll(session, "/", "_")+".json")
}
