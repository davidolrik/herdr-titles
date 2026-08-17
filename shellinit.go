package main

// `herdr-titles init <shell>` — the eval-able shell integration. It prints
// the same hook script that ships in shell/, with this binary's absolute
// path baked in, so users install with one line
//
//	eval "$(/path/to/herdr-titles init zsh)"
//
// instead of locating and sourcing the plugin's files. The on-disk hooks
// resolve the engine from their own sourced-file path; under eval there is no
// sourced file, so that one line (marked `# HWT_BIN`) is replaced here.

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed shell/hook.zsh shell/hook.bash shell/hook.fish
var hookFS embed.FS

// hwtBinMarker tags the single line in each hook that resolves the engine.
const hwtBinMarker = "# HWT_BIN"

// hwtShellIconMarker tags the line that sets the shell's icon glyph for the
// prompt title; `init` bakes the configured glyph in (icons on) or leaves it
// empty (icons off). Only zsh/bash have it — fish_title owns fish's title.
const hwtShellIconMarker = "# HWT_SHELL_ICON"

var initShells = []string{"zsh", "bash", "fish"}

// shellIconPrefix is the glyph-plus-space a prompt title starts with for
// shell, per the icons config: "" when icons are off, the style is "name",
// or the shell has no icon. Resolved through the same table the plugin uses
// for program names (custom map first, then builtins), so the shell's own
// tab matches every other tab.
func shellIconPrefix(shell string, cfg *TabsConfig) string {
	if !cfg.Icons.Enabled || cfg.Icons.Style == "name" {
		return ""
	}
	if glyph := knownProgramIcon(shell, &cfg.Icons); glyph != "" {
		return glyph + " "
	}
	return ""
}

// shellInitScript returns the hook for shell with binPath and the shell's
// icon prefix baked in.
func shellInitScript(shell, binPath, iconPrefix string) (string, error) {
	var ok bool
	for _, s := range initShells {
		if s == shell {
			ok = true
			break
		}
	}
	if !ok {
		return "", fmt.Errorf("usage: herdr-titles init <%s>", strings.Join(initShells, "|"))
	}
	raw, err := hookFS.ReadFile("shell/hook." + shell)
	if err != nil {
		return "", err
	}
	var assign string
	switch shell {
	case "fish":
		assign = "set -g _hwt_bin " + shellQuote(binPath)
	default:
		assign = "_hwt_bin=" + shellQuote(binPath)
	}
	lines := strings.Split(string(raw), "\n")
	replaced := false
	for i, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		switch {
		case strings.HasSuffix(trimmed, hwtBinMarker):
			lines[i] = assign
			replaced = true
		case strings.HasSuffix(trimmed, hwtShellIconMarker):
			// Keep the line's indentation; the value is single-quoted.
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = indent + "_hwt_shell_icon=" + shellQuote(iconPrefix)
		}
	}
	if !replaced {
		return "", fmt.Errorf("hook.%s has no %s line", shell, hwtBinMarker)
	}
	return strings.Join(lines, "\n"), nil
}

// shellQuote single-quotes s for zsh, bash and fish: inside single quotes
// nothing is special, and an embedded single quote is closed, escaped and
// reopened ('\” — fish accepts \' inside single quotes too, and this form
// parses in all three).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runShellInit prints the shell integration for `init <shell>`.
func runShellInit(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: herdr-titles init <%s>", strings.Join(initShells, "|"))
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// The shell can't read the HCL config, so resolve the shell's icon here
	// and bake it into the prompt title. A missing/invalid config just means
	// no icon — the integration must still emit.
	iconPrefix := ""
	if cfg, err := LoadConfig(filepath.Join(pluginConfigDir(), "config.hcl")); err == nil {
		iconPrefix = shellIconPrefix(args[0], cfg.Tabs)
	}
	script, err := shellInitScript(args[0], exe, iconPrefix)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(os.Stdout, script)
	return err
}
