package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
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
// ("<name> : <Context> @ <Location>") and appends the herdr space, tab, and
// attention summary. getenv() is used instead of env.* so the template still
// renders when Overseer is absent.
const defaultConfigHCL = `
template = "${coalesce(file("~/.local/var/herdr_window_title.${session}"), session)}${getenv("OVERSEER_CONTEXT_DISPLAY_NAME") != "" ? " : ${getenv("OVERSEER_CONTEXT_DISPLAY_NAME")}${getenv("OVERSEER_LOCATION_DISPLAY_NAME") != "" ? " @ ${getenv("OVERSEER_LOCATION_DISPLAY_NAME")}" : ""}" : ""} › ${pad_icons(space)} › ${pad_icons(tab)}${attention != "" ? " › ${attention}" : ""}"

env {
  ttl = "10s"
}

attention {
  statuses = ["blocked", "done", "unknown"]
  scope    = "all"
  # Herdr's own "symbols" status indicators (src/ui/status.rs) — the style
  # herdr uses when color isn't available, which a window title never has.
  icons = {
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
	Scope      string // "all" or "focused-space"
	Icons      map[string]string
}

type rawConfig struct {
	Template  hcl.Expression `hcl:"template,optional"`
	Env       *rawEnv        `hcl:"env,block"`
	Attention *rawAttention  `hcl:"attention,block"`
}

type rawEnv struct {
	Command []string `hcl:"command,optional"`
	TTL     string   `hcl:"ttl,optional"`
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
		Template: raw.Template,
		Scope:    "all",
		Statuses: defaults.Attention.Statuses,
		Icons:    defaults.Attention.Icons,
		EnvTTL:   10 * time.Second,
	}
	if cfg.Template == nil {
		cfg.Template = defaults.Template
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	cfg.EnvCommand = []string{shell, "-ilc", "env -0"}

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
	if cfg.Scope != "all" && cfg.Scope != "focused-space" {
		return nil, fmt.Errorf(`%s: attention.scope must be "all" or "focused-space", got %q`, path, cfg.Scope)
	}

	return cfg, nil
}
