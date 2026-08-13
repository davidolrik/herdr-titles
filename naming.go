package main

// Tab-name computation, ported from naming.sh of qu8n/herdr-automatic-rename
// (MIT, (c) Quan Nguyen). Everything here is pure string-in/string-out.
//
// Naming rule: a tab is named after its foreground program (nvim, claude,
// git, ...). At a bare prompt, or while a quick throwaway command runs, it
// shows the shell name instead — or nothing at all with hide_shell, which
// hands the tab back to herdr's own numbering.
//
// FormatTabName is total: an empty result is a legitimate name (hide_shell).
// "Could not determine the program" is the CALLER's condition, signalled
// before ever calling in here — never by an empty string out of it.

import (
	"regexp"
	"slices"
	"strings"
)

// Substitution is one ordered rewrite applied to the final display string.
// Go RE2 syntax — a deliberate, documented dialect change from upstream's
// `sed -E` programs.
type Substitution struct {
	Pattern *regexp.Regexp
	Replace string
}

// IconsConfig controls the Nerd Font glyph prefix.
type IconsConfig struct {
	Enabled  bool
	Style    string // "name_and_icon" | "name" | "icon"
	Fallback string
	Map      map[string]string
}

// TabsConfig is the tab-naming section of the plugin configuration.
type TabsConfig struct {
	Enabled          bool
	ShowProgramArgs  bool
	MaxNameLen       int
	ShellName        string
	HideShell        bool
	Shells           []string
	NameOnlyPrograms []string
	IgnoredPrograms  []string
	Aliases          map[string]string
	Substitutions    []Substitution
	AgentTitles      bool
	AgentTitleMaxLen int
	// TerminalTitles names a non-agent tab after its pane's terminal title
	// (terminal_title_stripped) when one is set, in preference to the
	// foreground program name.
	TerminalTitles bool
	// WatchTitles enables the per-session daemon that follows agent title
	// changes (Claude /rename etc.) the moment they happen.
	WatchTitles bool
	Icons       IconsConfig
}

func applySubstitutions(s string, subs []Substitution) string {
	for _, sub := range subs {
		s = sub.Pattern.ReplaceAllString(s, sub.Replace)
	}
	return s
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max])
	}
	return s
}

// applyIcon prepends the program's glyph according to the icon style. The
// fallback glyph says nothing about the program, so under style "icon" it is
// discarded and the plain name kept (rg -> "rg", not "?"); "name_and_icon"
// still shows "? rg".
func applyIcon(program, name string, icons *IconsConfig) string {
	glyph := programIcon(program, icons)
	if icons.Style == "icon" && glyph != "" && glyph == icons.Fallback {
		glyph = ""
	}
	if glyph == "" {
		return name
	}
	switch icons.Style {
	case "icon":
		return glyph
	case "name":
		return name
	default: // name_and_icon
		return glyph + " " + name
	}
}

// interpreterRe matches script-interpreter process names, with or without a
// version suffix (python, python3.13, ruby, node, ...). Shells are deliberately
// excluded: they run scripts too briefly and variedly to be worth unwrapping.
var interpreterRe = regexp.MustCompile(`^(python|ruby|perl|node|bun|deno)[\d.]*$`)

// unwrapInterpreter resolves the tool a script interpreter is actually
// running: a Python console script like ansible-playbook has argv0 "python"
// and the real name in argv[1]. Non-interpreters and bare REPLs pass through.
// Interpreter flags are skipped; `-m module` names the module; inline code
// (`-c`/`-e`) keeps the interpreter's own name.
func unwrapInterpreter(prog string, argv []string) string {
	if !interpreterRe.MatchString(prog) || len(argv) < 2 {
		return prog
	}
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "-m":
			if i+1 < len(argv) {
				return argv[i+1]
			}
			return prog
		case arg == "-c" || arg == "-e":
			return prog
		case len(arg) > 0 && arg[0] == '-':
			continue
		default:
			if j := strings.LastIndex(arg, "/"); j >= 0 {
				arg = arg[j+1:]
			}
			return arg
		}
	}
	return prog
}

// FormatTabName computes the tab label for a foreground program. An empty
// program means a bare prompt (name by the shell).
func FormatTabName(program, cmdline string, cfg *TabsConfig) string {
	var name string
	isShell := false
	alias, aliased := cfg.Aliases[program]

	switch {
	case program == "":
		name = cfg.ShellName
		isShell = true
	case aliased:
		name = alias // user rename wins over every other rule
	case slices.Contains(cfg.Shells, program):
		name = program // a shell shows its own name
		isShell = true
	case program == cfg.ShellName:
		name = program // the login shell, even outside Shells (nu, tcsh, ...)
		isShell = true
	case slices.Contains(cfg.IgnoredPrograms, program):
		name = cfg.ShellName // quick tools: keep showing the shell
		isShell = true
	case slices.Contains(cfg.NameOnlyPrograms, program):
		name = applySubstitutions(program, cfg.Substitutions)
	case cfg.ShowProgramArgs && cmdline != "":
		name = applySubstitutions(cmdline, cfg.Substitutions)
	default:
		name = applySubstitutions(program, cfg.Substitutions)
	}

	// hide_shell drops the shell label entirely and lets herdr number the
	// tab. An explicit alias for a shell is a name the user asked for by
	// hand, so it survives (the alias arm leaves isShell false).
	if cfg.HideShell && isShell {
		return ""
	}

	// Icons annotate the program the tab is named after; a shell label never
	// gets one (precmd names an idle prompt with program == "", so a glyph
	// here would flip the label between "zsh" and "<glyph> zsh" every pass).
	// Comparing against ShellName additionally keeps a cmdline- or
	// alias-derived label of the same text plain.
	if cfg.Icons.Enabled && program != "" && !isShell && name != cfg.ShellName {
		name = applyIcon(program, name, &cfg.Icons)
	}

	return truncateRunes(name, cfg.MaxNameLen)
}

// FormatAgentTitle names a tab after an agent's session title (herdr's
// terminal_title_stripped) instead of the agent's program name. The icon
// still identifies the agent kind; titles get their own, longer limit.
func FormatAgentTitle(agentKind, title string, cfg *TabsConfig) string {
	name := title
	if cfg.Icons.Enabled {
		name = applyIcon(agentKind, name, &cfg.Icons)
	}
	return truncateRunes(name, cfg.AgentTitleMaxLen)
}

// FormatTerminalTitle names a tab after its pane's terminal title. The shell
// (or program) that set the title is presumed to have produced the desired
// text already, so no substitutions and no icon — the program is unknown here.
func FormatTerminalTitle(title string, cfg *TabsConfig) string {
	return truncateRunes(title, cfg.MaxNameLen)
}

// DefaultTabsConfig mirrors the ported defaults of naming.sh/icons.sh. The
// agent entries in NameOnlyPrograms are the executable names herdr 0.8.0
// itself detects as interactive agents, plus aider.
func DefaultTabsConfig() *TabsConfig {
	return &TabsConfig{
		Enabled:         true,
		ShowProgramArgs: false,
		MaxNameLen:      20,
		HideShell:       false,
		Shells:          []string{"zsh", "bash", "sh", "fish", "dash", "ksh"},
		NameOnlyPrograms: []string{
			"nvim", "vim", "vi", "view", "gvim", "git", "lazygit", "gitui", "lazydocker",
			"claude", "codex", "aider", "pi", "gemini", "cursor", "cursor-agent", "devin",
			"agy", "antigravity", "cline", "omp", "mastracode", "opencode", "copilot",
			"kimi", "kiro", "kiro-cli", "droid", "amp", "grok", "hermes", "kilo", "qodercli",
		},
		IgnoredPrograms: []string{
			"ls", "eza", "ll", "la", "cd", "z", "zoxide", "cat", "bat", "less", "more",
			"echo", "pwd", "clear", "which", "man", "head", "tail", "wc", "cp", "mv",
			"rm", "mkdir", "touch", "fzf", "sudo", "doas",
		},
		Aliases:          map[string]string{},
		AgentTitles:      true,
		AgentTitleMaxLen: 40,
		TerminalTitles:   false,
		WatchTitles:      true,
		Icons: IconsConfig{
			Enabled:  false,
			Style:    "name_and_icon",
			Fallback: "?",
			Map:      map[string]string{},
		},
	}
}
