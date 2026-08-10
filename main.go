// herdr-titles composes the terminal window title from a configurable
// HCL template fed with herdr state (focused space, focused tab, agent
// attention counts) and the user's shell environment, then pushes it through
// `herdr terminal title set`. Herdr spawns it once per subscribed event; every
// invocation is one idempotent reconcile.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const pluginID = "davidolrik.titles"

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func defaultDir(kind, name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, kind, name)
}

// The shell hooks invoke this binary directly, without herdr's HERDR_PLUGIN_*
// environment, so the fallbacks must name the same directories herdr passes
// to plugin-spawned runs: its managed per-plugin config and state paths.
func pluginConfigDir() string {
	return envOr("HERDR_PLUGIN_CONFIG_DIR",
		defaultDir(".config", filepath.Join("herdr", "plugins", "config", pluginID)))
}

func pluginStateDir() string {
	return envOr("HERDR_PLUGIN_STATE_DIR",
		defaultDir(".local/state", filepath.Join("herdr", "plugins", pluginID)))
}

// run wraps one full reconcile in the coalescing lock: event bursts collapse
// into the lock holder's rerun loop instead of queueing processes.
func run(event string) error {
	stateDir := pluginStateDir()
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	session := envOr("HERDR_SESSION", "default")
	return withLock(stateDir, session, func() error { return fullPass(event) })
}

// fullPass is one idempotent reconcile: tabs, then the window title. The
// caller holds the lock.
func fullPass(event string) error {
	configDir := pluginConfigDir()
	stateDir := pluginStateDir()

	cfg, err := LoadConfig(filepath.Join(configDir, "config.hcl"))
	if err != nil {
		return err
	}

	// The refresh action exists to pick up external changes (e.g. an Overseer
	// context switch), so it must not serve cached environment.
	bypassCache := event == "refresh"
	harvested, err := HarvestEnv(cfg.EnvCommand, filepath.Join(stateDir, "env.cache"), cfg.EnvTTL, bypassCache)
	if err != nil {
		return err
	}
	// The harvested shell environment wins; the process env fills in what only
	// the plugin engine knows (HERDR_* variables).
	env := map[string]string{}
	for _, entry := range os.Environ() {
		for i := 0; i < len(entry); i++ {
			if entry[i] == '=' {
				env[entry[:i]] = entry[i+1:]
				break
			}
		}
	}
	for k, v := range harvested {
		env[k] = v
	}

	herdrBin := envOr("HERDR_BIN_PATH", "herdr")
	snap, err := FetchSnapshot(herdrBin)
	if err != nil {
		return err
	}

	session := envOr("HERDR_SESSION", "default")

	// Tabs first, so the window title's ${tab} sees the fresh label. The
	// reset action re-adopts the invoking tab regardless of its opt-out.
	if cfg.Tabs.Enabled {
		forceTab := ""
		if event == "reset" {
			forceTab = envOr("HERDR_TAB_ID", contextTabID())
			if forceTab == "" {
				forceTab = snap.FocusedTabID
			}
		}
		statePath := tabStatePath(stateDir, session)
		tabStates := LoadTabStates(statePath)
		ReconcileTabs(herdrBin, snap, cfg.Tabs, tabStates, forceTab)
		if err := SaveTabStates(statePath, tabStates); err != nil {
			return err
		}
	}

	title, err := ComposeTitle(cfg, snap, session, env)
	if err != nil {
		return err
	}

	// The state dir is shared by every herdr session's server, so the record
	// of the last pushed title must be kept per session.
	_, err = ApplyTitle(herdrBin, filepath.Join(stateDir, "last_title."+session), title)
	return err
}

// contextTabID pulls the invoking tab out of herdr's action context JSON.
func contextTabID() string {
	var ctx struct {
		TabID string `json:"tab_id"`
	}
	if err := json.Unmarshal([]byte(os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")), &ctx); err != nil {
		return ""
	}
	return ctx.TabID
}

// runFast is the shell-hook path: rename just the invoking tab, right now.
// mode "preexec" carries the typed command line (plus a "shell" marker when
// the word is a construct and the pane's real process must be sampled); mode
// "precmd" means back-at-the-prompt, optionally naming the hook's shell.
func runFast(mode string, args []string) error {
	tabID := os.Getenv("HERDR_TAB_ID")
	if tabID == "" {
		return nil // not inside a herdr pane
	}
	cfg, err := LoadConfig(filepath.Join(pluginConfigDir(), "config.hcl"))
	if err != nil {
		return err
	}
	tabs := cfg.Tabs
	if !tabs.Enabled {
		return nil
	}

	var prog, cmdline string
	sampled := false
	switch mode {
	case "preexec":
		if len(args) == 0 {
			return nil
		}
		cmdline = args[0]
		if len(args) > 1 && args[1] == "shell" {
			sampled = true
		} else {
			prog = cmdline
			if i := strings.IndexByte(prog, ' '); i >= 0 {
				prog = prog[:i]
			}
			if i := strings.LastIndex(prog, "/"); i >= 0 {
				prog = prog[i+1:]
			}
		}
	case "precmd":
		if len(args) > 0 && args[0] != "" {
			tabs.ShellName = args[0]
		}
	}

	// The shell construct just started; give the real foreground process a
	// beat to appear. Deliberately before taking the lock.
	if sampled {
		time.Sleep(200 * time.Millisecond)
	}

	stateDir := pluginStateDir()
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}

	// First pass under the lock is the single-tab fast rename; any rerun flag
	// raised meanwhile escalates to a full reconcile, which is a superset —
	// a structural event that raced this hook still gets handled.
	first := true
	return withLock(stateDir, envOr("HERDR_SESSION", "default"), func() error {
		if !first {
			return fullPass("rerun")
		}
		first = false

		herdrBin := envOr("HERDR_BIN_PATH", "herdr")
		if sampled {
			p, c, err := paneProgram(herdrBin, os.Getenv("HERDR_PANE_ID"))
			if err != nil || p == "" {
				return nil // never guess
			}
			prog, cmdline = p, c
		}

		name := FormatTabName(prog, cmdline, tabs)
		if name == "" && !tabs.HideShell {
			return nil
		}
		label, ok := tabLabel(herdrBin, tabID)
		if !ok {
			return nil
		}

		statePath := tabStatePath(stateDir, envOr("HERDR_SESSION", "default"))
		states := LoadTabStates(statePath)
		if !states.Eligible(tabID, label, name, false) {
			return SaveTabStates(statePath, states) // Eligible may record an opt-out
		}
		if name != label {
			renameTab(herdrBin, tabID, name)
		}
		states[tabID] = TabState{Auto: name, Enabled: true}
		return SaveTabStates(statePath, states)
	})
}

// runInit writes the documented default config file and reports where.
func runInit() error {
	path, err := WriteDefaultConfig(pluginConfigDir())
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", path)
	return nil
}

func main() {
	event := "startup"
	if len(os.Args) > 1 {
		event = os.Args[1]
	}
	var err error
	switch event {
	case "init":
		err = runInit()
	case "refresh-all":
		err = runRefreshAll()
	case "preexec", "precmd":
		err = runFast(event, os.Args[2:])
	default:
		err = run(event)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "herdr-titles (%s): %v\n", event, err)
		os.Exit(1)
	}
}
