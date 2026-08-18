package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
)

// exampleConfigHCL is the fully-documented example config, embedded so the
// `init` subcommand and the file shipped in the repo can never drift apart.
//
//go:embed config.example.hcl
var exampleConfigHCL []byte

// WriteDefaultConfig writes the documented example config as config.hcl inside
// configDir (created if needed) and returns the written path. An existing
// config.hcl is never overwritten.
func WriteDefaultConfig(configDir string) (string, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(configDir, "config.hcl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return "", fmt.Errorf("%s already exists; delete it first to regenerate", path)
		}
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(exampleConfigHCL); err != nil {
		return "", err
	}
	return path, nil
}

// defaultConfigHCL is used when no config file exists, and its pieces fill in
// anything a user config omits. The template mirrors the pre-plugin title
// ("<name> : <Context> @ <Location>") and appends the herdr workspace, tab, and
// attention summary. getenv() is used instead of env.* so the template still
// renders when Overseer is absent.
const defaultConfigHCL = `
template = "${coalesce(file("~/.local/var/herdr_window_title.${session}"), session)}${getenv("OVERSEER_CONTEXT_DISPLAY_NAME") != "" ? " : ${getenv("OVERSEER_CONTEXT_DISPLAY_NAME")}${getenv("OVERSEER_LOCATION_DISPLAY_NAME") != "" ? " @ ${getenv("OVERSEER_LOCATION_DISPLAY_NAME")}" : ""}" : ""} › ${pad_icons(workspace)} › ${pad_icons(tab)}${attention != "" ? " › ${attention}" : ""}"

env {
  ttl = "10s"
}

attention {
  statuses = ["blocked", "done", "unknown"]
  scope    = "all"
  # Herdr's own "symbols" status indicators (src/ui/status.rs) — the style
  # herdr uses when color isn't available, which a window title never has.
  # Every state has an icon; only the statuses listed above are shown.
  icons = {
    idle    = "○"
    working = "◐"
    blocked = "×"
    done    = "✓"
    unknown = "·"
  }
}
`

// Config is the fully-resolved plugin configuration.
type Config struct {
	Template   hcl.Expression
	EnvCommand []string
	EnvTTL     time.Duration
	Statuses   []string
	Scope      string // "all" or "focused-workspace"
	Icons      map[string]string
	Tabs       *TabsConfig
	// EnvWatchFiles are files whose mtime changes should trigger a
	// cache-bypassing title refresh (the watch daemon polls them).
	EnvWatchFiles []string
	// TitlebarIcons keeps nerd-font glyphs in the WINDOW title. Off by
	// default on macOS: its title bar renders in the system font, where
	// private-use glyphs are tofu. Tab labels are unaffected.
	TitlebarIcons bool
}

type rawConfig struct {
	Template      hcl.Expression `hcl:"template,optional"`
	TitlebarIcons *bool          `hcl:"titlebar_icons,optional"`
	Env           *rawEnv        `hcl:"env,block"`
	Attention     *rawAttention  `hcl:"attention,block"`
	Tabs          *rawTabs       `hcl:"tabs,block"`
}

// Pointer fields distinguish "absent" from a deliberate zero value (enabled =
// false, fallback = "").
type rawTabs struct {
	Enabled          *bool             `hcl:"enabled,optional"`
	ShowProgramArgs  *bool             `hcl:"show_program_args,optional"`
	MaxNameLen       *int              `hcl:"max_name_len,optional"`
	ShellName        string            `hcl:"shell_name,optional"`
	HideShell        *bool             `hcl:"hide_shell,optional"`
	Shells           []string          `hcl:"shells,optional"`
	NameOnlyPrograms []string          `hcl:"name_only_programs,optional"`
	IgnoredPrograms  []string          `hcl:"ignored_programs,optional"`
	Aliases          map[string]string `hcl:"aliases,optional"`
	Substitutions    []rawSubstitution `hcl:"substitute,block"`
	AgentTitles      *bool             `hcl:"agent_titles,optional"`
	TerminalTitles   *bool             `hcl:"terminal_titles,optional"`
	WatchTitles      *bool             `hcl:"watch_titles,optional"`
	AgentTitleMaxLen *int              `hcl:"agent_title_max_len,optional"`
	Icons            *rawTabIcons      `hcl:"icons,block"`
}

type rawSubstitution struct {
	Pattern string `hcl:"pattern"`
	Replace string `hcl:"replace"`
}

type rawTabIcons struct {
	Enabled  *bool             `hcl:"enabled,optional"`
	Style    string            `hcl:"style,optional"`
	Fallback *string           `hcl:"fallback,optional"`
	Map      map[string]string `hcl:"map,optional"`
}

type rawEnv struct {
	Command    []string `hcl:"command,optional"`
	TTL        string   `hcl:"ttl,optional"`
	WatchFiles []string `hcl:"watch_files,optional"`
}

type rawAttention struct {
	Statuses []string          `hcl:"statuses,optional"`
	Scope    string            `hcl:"scope,optional"`
	Icons    map[string]string `hcl:"icons,optional"`
}

func parseRaw(filename string, src []byte) (*rawConfig, error) {
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCL(src, filename)
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s: %w", filename, diags)
	}
	var raw rawConfig
	// Template stays an unevaluated hcl.Expression, so no EvalContext is
	// needed here; runtime variables are bound per invocation in title.go.
	if diags := gohcl.DecodeBody(file.Body, nil, &raw); diags.HasErrors() {
		return nil, fmt.Errorf("%s: %w", filename, diags)
	}
	return &raw, nil
}

// LoadConfig reads an HCL config file, falling back to built-in defaults for a
// missing file and for any omitted attribute or block.
func LoadConfig(path string) (*Config, error) {
	defaults, err := parseRaw("default config", []byte(defaultConfigHCL))
	if err != nil {
		return nil, fmt.Errorf("internal default config is invalid: %w", err)
	}

	raw := defaults
	if src, readErr := os.ReadFile(path); readErr == nil {
		if raw, err = parseRaw(path, src); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(readErr) {
		return nil, readErr
	}

	cfg := &Config{
		Template:      raw.Template,
		TitlebarIcons: runtime.GOOS != "darwin",
		Scope:         "all",
		Statuses:      defaults.Attention.Statuses,
		Icons:         defaults.Attention.Icons,
		EnvTTL:        10 * time.Second,
	}
	if cfg.Template == nil {
		cfg.Template = defaults.Template
	}
	if raw.TitlebarIcons != nil {
		cfg.TitlebarIcons = *raw.TitlebarIcons
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	// A login shell, so ~/.zshenv and ~/.zprofile (or the bash/fish
	// equivalents) are read; NOT interactive — see the note in the example
	// config: interactive startup drags in prompt/plugin machinery that a
	// probe doesn't need, and an interactive zsh grabs the controlling tty.
	cfg.EnvCommand = []string{shell, "-lc", "env -0"}

	if raw.Env != nil {
		if len(raw.Env.Command) > 0 {
			cfg.EnvCommand = raw.Env.Command
		}
		if raw.Env.TTL != "" {
			ttl, err := time.ParseDuration(raw.Env.TTL)
			if err != nil {
				return nil, fmt.Errorf("%s: env.ttl: %w", path, err)
			}
			cfg.EnvTTL = ttl
		}
		for _, f := range raw.Env.WatchFiles {
			cfg.EnvWatchFiles = append(cfg.EnvWatchFiles, expandTilde(f))
		}
	}

	if raw.Attention != nil {
		if len(raw.Attention.Statuses) > 0 {
			cfg.Statuses = raw.Attention.Statuses
		}
		if raw.Attention.Scope != "" {
			cfg.Scope = raw.Attention.Scope
		}
		if len(raw.Attention.Icons) > 0 {
			cfg.Icons = raw.Attention.Icons
		}
	}
	if cfg.Scope != "all" && cfg.Scope != "focused-workspace" {
		return nil, fmt.Errorf(`%s: attention.scope must be "all" or "focused-workspace", got %q`, path, cfg.Scope)
	}

	cfg.Tabs, err = resolveTabs(raw.Tabs, path, shell)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

// resolveTabs merges a parsed tabs block over the built-in defaults.
func resolveTabs(raw *rawTabs, path, shell string) (*TabsConfig, error) {
	tabs := DefaultTabsConfig()
	tabs.ShellName = filepath.Base(shell)
	if tabs.ShellName == "" || tabs.ShellName == "." {
		tabs.ShellName = "zsh"
	}
	if raw == nil {
		return tabs, nil
	}

	setBool := func(dst *bool, src *bool) {
		if src != nil {
			*dst = *src
		}
	}
	setBool(&tabs.Enabled, raw.Enabled)
	setBool(&tabs.ShowProgramArgs, raw.ShowProgramArgs)
	setBool(&tabs.HideShell, raw.HideShell)
	setBool(&tabs.AgentTitles, raw.AgentTitles)
	setBool(&tabs.TerminalTitles, raw.TerminalTitles)
	setBool(&tabs.WatchTitles, raw.WatchTitles)
	// terminal_titles rides the daemon: it is the single writer that follows
	// pane titles, and the shell hooks only publish titles for it to apply.
	// With no daemon there is nothing to apply them, so refuse the
	// combination outright rather than degrade silently.
	if tabs.TerminalTitles && !tabs.WatchTitles {
		return nil, fmt.Errorf("%s: tabs.terminal_titles = true requires tabs.watch_titles = true (the watch daemon applies terminal titles)", path)
	}
	if raw.MaxNameLen != nil {
		tabs.MaxNameLen = *raw.MaxNameLen
	}
	if raw.AgentTitleMaxLen != nil {
		tabs.AgentTitleMaxLen = *raw.AgentTitleMaxLen
	}
	if raw.ShellName != "" {
		tabs.ShellName = raw.ShellName
	}
	if raw.Shells != nil {
		tabs.Shells = raw.Shells
	}
	if raw.NameOnlyPrograms != nil {
		tabs.NameOnlyPrograms = raw.NameOnlyPrograms
	}
	if raw.IgnoredPrograms != nil {
		tabs.IgnoredPrograms = raw.IgnoredPrograms
	}
	if raw.Aliases != nil {
		tabs.Aliases = raw.Aliases
	}
	for _, sub := range raw.Substitutions {
		pattern, err := regexp.Compile(sub.Pattern)
		if err != nil {
			return nil, fmt.Errorf("%s: tabs.substitute pattern %q: %w", path, sub.Pattern, err)
		}
		tabs.Substitutions = append(tabs.Substitutions, Substitution{Pattern: pattern, Replace: sub.Replace})
	}
	if raw.Icons != nil {
		setBool(&tabs.Icons.Enabled, raw.Icons.Enabled)
		if raw.Icons.Style != "" {
			tabs.Icons.Style = raw.Icons.Style
		}
		if raw.Icons.Fallback != nil {
			tabs.Icons.Fallback = *raw.Icons.Fallback
		}
		if raw.Icons.Map != nil {
			tabs.Icons.Map = raw.Icons.Map
		}
	}
	return tabs, nil
}
