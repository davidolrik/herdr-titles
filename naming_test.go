package main

import (
	"regexp"
	"strings"
	"testing"
)

// namingConfig returns a TabsConfig mirroring the ported defaults, with icons
// off; tests flip individual knobs.
func namingConfig() *TabsConfig {
	cfg := DefaultTabsConfig()
	cfg.ShellName = "zsh"
	return cfg
}

func TestFormatTabNameLadder(t *testing.T) {
	cfg := namingConfig()
	cfg.Aliases = map[string]string{"lazygit": "lg"}
	cases := []struct{ name, prog, cmdline, want string }{
		{"bare prompt shows shell", "", "", "zsh"},
		{"alias wins", "lazygit", "lazygit -p", "lg"},
		{"shell shows own name", "bash", "bash", "bash"},
		{"ignored keeps shell", "ls", "ls -la", "zsh"},
		{"name-only drops args", "nvim", "nvim main.go", "nvim"},
		{"regular program name only by default", "psql", "psql -h db", "psql"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatTabName(tc.prog, tc.cmdline, cfg); got != tc.want {
				t.Errorf("FormatTabName(%q, %q) = %q, want %q", tc.prog, tc.cmdline, got, tc.want)
			}
		})
	}
}

func TestFormatTabNameLoginShellOutsideList(t *testing.T) {
	cfg := namingConfig()
	cfg.ShellName = "nu" // login shell not in Shells
	if got := FormatTabName("nu", "nu", cfg); got != "nu" {
		t.Errorf("login shell outside SHELLS = %q, want nu", got)
	}
}

func TestFormatTabNameShowProgramArgs(t *testing.T) {
	cfg := namingConfig()
	cfg.ShowProgramArgs = true
	if got := FormatTabName("psql", "psql -h db", cfg); got != "psql -h db" {
		t.Errorf("with args = %q, want full cmdline", got)
	}
	// name-only programs still drop args
	if got := FormatTabName("nvim", "nvim main.go", cfg); got != "nvim" {
		t.Errorf("name-only with args enabled = %q, want nvim", got)
	}
}

func TestFormatTabNameSubstitutions(t *testing.T) {
	cfg := namingConfig()
	cfg.ShowProgramArgs = true
	cfg.Substitutions = []Substitution{
		{Pattern: regexp.MustCompile(`.*ipython([32])`), Replace: "ipython$1"},
	}
	if got := FormatTabName("ipython3", "/usr/local/bin/ipython3 --pylab", cfg); got != "ipython3 --pylab" {
		t.Errorf("substitution = %q, want %q", got, "ipython3 --pylab")
	}
}

func TestFormatTabNameHideShell(t *testing.T) {
	cfg := namingConfig()
	cfg.HideShell = true
	cfg.Aliases = map[string]string{"fish": "sh"}
	for _, tc := range []struct{ name, prog, want string }{
		{"bare prompt hidden", "", ""},
		{"shell hidden", "zsh", ""},
		{"ignored hidden", "ls", ""},
		{"aliased shell survives", "fish", "sh"},
		{"program unaffected", "nvim", "nvim"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatTabName(tc.prog, tc.prog, cfg); got != tc.want {
				t.Errorf("FormatTabName(%q) = %q, want %q", tc.prog, got, tc.want)
			}
		})
	}
}

func TestFormatTabNameIcons(t *testing.T) {
	cfg := namingConfig()
	cfg.Icons.Enabled = true

	// Exact glyph bytes are load-bearing (they were once silently stripped
	// upstream); assert codepoints, not just non-emptiness.
	if got := FormatTabName("nvim", "nvim", cfg); got != "\uE6AE nvim" {
		t.Errorf("nvim icon = %q, want %q", got, "\uE6AE nvim")
	}
	if got := FormatTabName("claude", "claude", cfg); got != "\U000F06A9 claude" {
		t.Errorf("claude icon = %q, want %q", got, "\U000F06A9 claude")
	}

	// No icon on shell labels — precmd would flip between "zsh" and glyph+zsh.
	if got := FormatTabName("zsh", "zsh", cfg); got != "zsh" {
		t.Errorf("shell got an icon: %q", got)
	}
	if got := FormatTabName("ls", "ls", cfg); got != "zsh" {
		t.Errorf("ignored program got an icon: %q", got)
	}

	cfg.Icons.Style = "icon"
	if got := FormatTabName("nvim", "nvim", cfg); got != "\uE6AE" {
		t.Errorf("style=icon = %q, want glyph only", got)
	}
	// Fallback glyph says nothing under style=icon: keep the plain name.
	if got := FormatTabName("rg", "rg", cfg); got != "rg" {
		t.Errorf("style=icon fallback = %q, want plain rg", got)
	}

	cfg.Icons.Style = "name_and_icon"
	if got := FormatTabName("rg", "rg", cfg); got != "? rg" {
		t.Errorf("fallback name_and_icon = %q, want %q", got, "? rg")
	}
	cfg.Icons.Fallback = ""
	if got := FormatTabName("rg", "rg", cfg); got != "rg" {
		t.Errorf("empty fallback = %q, want plain rg", got)
	}

	cfg.Icons.Style = "name"
	if got := FormatTabName("nvim", "nvim", cfg); got != "nvim" {
		t.Errorf("style=name = %q, want nvim", got)
	}

	cfg.Icons.Style = "name_and_icon"
	cfg.Icons.Map = map[string]string{"nvim": "N"}
	if got := FormatTabName("nvim", "nvim", cfg); got != "N nvim" {
		t.Errorf("icon map override = %q, want %q", got, "N nvim")
	}
}

func TestFormatTabNameTruncation(t *testing.T) {
	cfg := namingConfig()
	cfg.Aliases = map[string]string{"x": strings.Repeat("é", 30)}
	got := FormatTabName("x", "x", cfg)
	if len([]rune(got)) != 20 {
		t.Errorf("truncated length = %d runes, want 20", len([]rune(got)))
	}
	if !strings.HasPrefix(strings.Repeat("é", 30), got) {
		t.Errorf("truncation broke runes: %q", got)
	}
}

func TestFormatTerminalTitle(t *testing.T) {
	cfg := namingConfig()
	cfg.MaxNameLen = 10
	// Substitutions apply; no icon even when enabled; truncated last.
	cfg.Substitutions = []Substitution{{Pattern: regexp.MustCompile("make"), Replace: "MAKE"}}
	cfg.Icons.Enabled = true
	if got := FormatTerminalTitle("make -j all target", cfg); got != "MAKE -j al" {
		t.Errorf("FormatTerminalTitle = %q, want substituted title truncated to 10 runes", got)
	}
}

func TestTitleTabName(t *testing.T) {
	cases := []struct {
		name                    string
		agentKind, title        string
		agentTitles, termTitles bool
		want                    string
		ok                      bool
	}{
		// 25 runes: the agent arm keeps all of it (limit 40), the terminal
		// arm truncates to max_name_len (20) — so precedence is observable.
		{"agent title wins", "claude", "Fix the flaky yaml tests!", true, true, "Fix the flaky yaml tests!", true},
		{"agent titles off -> terminal", "claude", "Fix the flaky yaml tests!", false, true, "Fix the flaky yaml t", true},
		{"both off", "claude", "Fix the flaky yaml tests!", false, false, "", false},
		{"plain pane terminal title", "", "make all", true, true, "make all", true},
		{"plain pane, terminal off", "", "make all", true, false, "", false},
		{"empty title always falls back", "claude", "", true, true, "", false},
	}
	for _, c := range cases {
		cfg := namingConfig()
		cfg.AgentTitles = c.agentTitles
		cfg.TerminalTitles = c.termTitles
		got, ok := titleTabName(c.agentKind, c.title, cfg)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: titleTabName(%q, %q) = %q, %v; want %q, %v",
				c.name, c.agentKind, c.title, got, ok, c.want, c.ok)
		}
	}
}

func TestUnwrapInterpreter(t *testing.T) {
	cases := []struct {
		name string
		prog string
		argv []string
		want string
	}{
		{"python console script", "python", []string{"/v/bin/python", "/v/bin/ansible-playbook", "server-upgrade.yml"}, "ansible-playbook"},
		{"versioned interpreter", "python3.13", []string{"/usr/bin/python3.13", "/usr/local/bin/aws", "s3", "ls"}, "aws"},
		{"bare REPL stays", "python", []string{"python"}, "python"},
		{"flags skipped", "python", []string{"python", "-u", "script.py"}, "script.py"},
		{"-m names the module", "python", []string{"python", "-m", "http.server", "8000"}, "http.server"},
		{"-c inline code stays", "python", []string{"python", "-c", "print(1)"}, "python"},
		{"-e inline code stays", "perl", []string{"perl", "-e", "print 1"}, "perl"},
		{"node script", "node", []string{"/usr/bin/node", "server.js"}, "server.js"},
		{"ruby script", "ruby", []string{"ruby", "/v/bin/rails", "console"}, "rails"},
		{"non-interpreter passthrough", "nvim", []string{"nvim", "main.go"}, "nvim"},
		{"interpreter-prefixed name passthrough", "nodemon", []string{"nodemon", "app.js"}, "nodemon"},
		{"empty argv passthrough", "python", nil, "python"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := unwrapInterpreter(tc.prog, tc.argv); got != tc.want {
				t.Errorf("unwrapInterpreter(%q, %v) = %q, want %q", tc.prog, tc.argv, got, tc.want)
			}
		})
	}
}

func TestFormatAgentTitle(t *testing.T) {
	cfg := namingConfig()
	cfg.Icons.Enabled = true
	title := "Build herdr plugin for terminal title updates"

	got := FormatAgentTitle("claude", title, cfg)
	want := string([]rune("\U000F06A9 " + title)[:40]) // 40 runes incl. glyph and space
	if got != want {
		t.Errorf("agent title = %q, want %q", got, want)
	}
	if n := len([]rune(got)); n != 40 {
		t.Errorf("agent title length = %d runes, want 40", n)
	}

	cfg.Icons.Enabled = false
	if got := FormatAgentTitle("claude", "Short title", cfg); got != "Short title" {
		t.Errorf("icons off = %q, want bare title", got)
	}
}
