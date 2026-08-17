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

var initShells = []string{"zsh", "bash", "fish"}

// shellInitScript returns the hook for shell with binPath baked in.
func shellInitScript(shell, binPath string) (string, error) {
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
		if strings.HasSuffix(strings.TrimRight(line, " \t"), hwtBinMarker) {
			lines[i] = assign
			replaced = true
			break
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
	script, err := shellInitScript(args[0], exe)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(os.Stdout, script)
	return err
}
