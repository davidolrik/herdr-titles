package main

import (
	"path/filepath"
	"testing"
)

func TestTabStatesEligible(t *testing.T) {
	cases := []struct {
		name     string
		initial  TabStates
		label    string
		computed string
		force    bool
		want     bool
		// expected stored enabled flag afterwards (nil = no entry expected)
		wantEnabled *bool
	}{
		{name: "first sight empty label adopts", initial: TabStates{}, label: "", computed: "nvim", want: true},
		{name: "first sight numeric placeholder adopts", initial: TabStates{}, label: "3", computed: "nvim", want: true},
		{name: "first sight label matching computed adopts", initial: TabStates{}, label: " nvim", computed: " nvim", want: true},
		{name: "first sight hand-named opts out", initial: TabStates{}, label: "my tab", computed: "nvim", want: false, wantEnabled: boolPtr(false)},
		{name: "opted out stays out", initial: TabStates{"t": {Enabled: false}}, label: "my tab", computed: "nvim", want: false},
		{name: "opted out re-adopts on cleared label", initial: TabStates{"t": {Enabled: false}}, label: "", computed: "nvim", want: true},
		// Herdr's rename UI refuses an empty name, and dropping a custom name
		// reverts the label to the tab number — so a numeric placeholder on an
		// opted-out tab means the custom name is gone and must re-adopt.
		{name: "opted out re-adopts on numeric placeholder", initial: TabStates{"t": {Enabled: false}}, label: "4", computed: "nvim", want: true},
		// Herdr's rename UI rejects an empty name but accepts spaces, so
		// whitespace is the "clear this tab" gesture the UI can express.
		{name: "opted out re-adopts on single space", initial: TabStates{"t": {Enabled: false}}, label: " ", computed: "nvim", want: true},
		{name: "opted out re-adopts on several spaces", initial: TabStates{"t": {Enabled: false}}, label: "   ", computed: "nvim", want: true},
		{name: "first sight whitespace label adopts", initial: TabStates{}, label: "  ", computed: "nvim", want: true},
		{name: "owned label matching auto stays eligible", initial: TabStates{"t": {Auto: "nvim", Enabled: true}}, label: "nvim", computed: "nvim", want: true},
		{name: "owned cleared label re-adopts", initial: TabStates{"t": {Auto: "nvim", Enabled: true}}, label: "", computed: "nvim", want: true},
		{name: "owned hide-shell tab placeholder ok", initial: TabStates{"t": {Auto: "", Enabled: true}}, label: "7", computed: "", want: true},
		{name: "owned user rename opts out", initial: TabStates{"t": {Auto: "nvim", Enabled: true}}, label: "my tab", computed: "nvim", want: false, wantEnabled: boolPtr(false)},
		{name: "force overrides opt-out", initial: TabStates{"t": {Enabled: false}}, label: "my tab", computed: "nvim", force: true, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.initial.Eligible("t", tc.label, tc.computed, tc.force)
			if got != tc.want {
				t.Errorf("Eligible = %v, want %v", got, tc.want)
			}
			if tc.wantEnabled != nil {
				st, ok := tc.initial["t"]
				if !ok {
					t.Fatal("expected a recorded state entry")
				}
				if st.Enabled != *tc.wantEnabled {
					t.Errorf("stored enabled = %v, want %v", st.Enabled, *tc.wantEnabled)
				}
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

func TestTabStatesPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tabstate.test.json")

	s := TabStates{
		"w1:t1": {Auto: "nvim", Enabled: true},
		"w1:t2": {Auto: "", Enabled: false},
	}
	if err := SaveTabStates(path, s); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded := LoadTabStates(path)
	if len(loaded) != 2 {
		t.Fatalf("loaded %d entries, want 2", len(loaded))
	}
	// enabled:false must round-trip as an EXISTING opted-out entry, never as
	// first-sight (the upstream jq `//` pitfall).
	if st, ok := loaded["w1:t2"]; !ok || st.Enabled {
		t.Errorf("opted-out entry lost: ok=%v st=%+v", ok, st)
	}

	// Missing file: empty, usable map.
	empty := LoadTabStates(filepath.Join(t.TempDir(), "nope.json"))
	if len(empty) != 0 {
		t.Errorf("missing file loaded %d entries", len(empty))
	}

	// Prune to seen tabs.
	s.Prune([]string{"w1:t1"})
	if _, ok := s["w1:t2"]; ok {
		t.Error("prune kept unseen tab")
	}
	if _, ok := s["w1:t1"]; !ok {
		t.Error("prune dropped seen tab")
	}
}

func TestLoadConfigTabsDefaults(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	tabs := cfg.Tabs
	if !tabs.Enabled || tabs.MaxNameLen != 20 || tabs.HideShell || tabs.ShowProgramArgs {
		t.Errorf("tab defaults wrong: %+v", tabs)
	}
	if tabs.ShellName != "zsh" {
		t.Errorf("ShellName = %q, want zsh from $SHELL", tabs.ShellName)
	}
	if !tabs.AgentTitles || tabs.AgentTitleMaxLen != 40 {
		t.Errorf("agent title defaults wrong: %+v", tabs)
	}
	if tabs.Icons.Enabled || tabs.Icons.Style != "name_and_icon" || tabs.Icons.Fallback != "?" {
		t.Errorf("icon defaults wrong: %+v", tabs.Icons)
	}
	if len(tabs.Shells) == 0 || len(tabs.NameOnlyPrograms) == 0 || len(tabs.IgnoredPrograms) == 0 {
		t.Error("default program lists empty")
	}
}

func TestLoadConfigTabsBlock(t *testing.T) {
	path := writeConfig(t, `
template = "x"
tabs {
  enabled           = false
  show_program_args = true
  max_name_len      = 30
  shell_name        = "fish"
  hide_shell        = true
  shells            = ["fish"]
  aliases           = { lazygit = "lg" }
  agent_titles      = false
  agent_title_max_len = 60

  substitute {
    pattern = ".*ipython([32])"
    replace = "ipython$1"
  }

  icons {
    enabled  = true
    style    = "icon"
    fallback = ""
    map      = { nvim = "N" }
  }
}
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	tabs := cfg.Tabs
	if tabs.Enabled || !tabs.ShowProgramArgs || tabs.MaxNameLen != 30 || !tabs.HideShell {
		t.Errorf("parsed tabs wrong: %+v", tabs)
	}
	if tabs.ShellName != "fish" || len(tabs.Shells) != 1 || tabs.Aliases["lazygit"] != "lg" {
		t.Errorf("parsed lists wrong: %+v", tabs)
	}
	if tabs.AgentTitles || tabs.AgentTitleMaxLen != 60 {
		t.Errorf("agent title settings wrong: %+v", tabs)
	}
	if len(tabs.Substitutions) != 1 || tabs.Substitutions[0].Replace != "ipython$1" {
		t.Fatalf("substitutions wrong: %+v", tabs.Substitutions)
	}
	if got := tabs.Substitutions[0].Pattern.ReplaceAllString("/bin/ipython3", "ipython$1"); got != "ipython3" {
		t.Errorf("compiled pattern misbehaves: %q", got)
	}
	// Explicit empty fallback must survive (absent would mean "?").
	if !tabs.Icons.Enabled || tabs.Icons.Style != "icon" || tabs.Icons.Fallback != "" || tabs.Icons.Map["nvim"] != "N" {
		t.Errorf("parsed icons wrong: %+v", tabs.Icons)
	}
	// Unset lists keep defaults.
	if len(tabs.IgnoredPrograms) == 0 {
		t.Error("unset ignored_programs lost its default")
	}
}

func TestLoadConfigTabsBadSubstitution(t *testing.T) {
	path := writeConfig(t, `
template = "x"
tabs {
  substitute {
    pattern = "("
    replace = "x"
  }
}
`)
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for invalid substitution regex")
	}
}
