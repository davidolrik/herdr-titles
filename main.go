// herdr-titles composes the terminal window title from a configurable
// HCL template fed with herdr state (focused space, focused tab, agent
// attention counts) and the user's shell environment, then pushes it through
// `herdr terminal title set`. Herdr spawns it once per subscribed event; every
// invocation is one idempotent reconcile.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
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
	return withLock(stateDir, session, func() error {
		return pass(event, true, event == "refresh")
	})
}

// runTitleOnly is the daemon's cheap pass: window title only, no tab
// reconcile and therefore no per-tab process-info subprocesses.
func runTitleOnly(event string, bypassEnvCache bool) error {
	stateDir := pluginStateDir()
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	session := envOr("HERDR_SESSION", "default")
	return withLock(stateDir, session, func() error {
		return pass(event, false, bypassEnvCache)
	})
}

// pass is one idempotent reconcile: optionally tabs, then the window title.
// The caller holds the per-session lock.
func pass(event string, withTabs, bypassEnvCache bool) error {
	configDir := pluginConfigDir()
	stateDir := pluginStateDir()

	cfg, err := LoadConfig(filepath.Join(configDir, "config.hcl"))
	if err != nil {
		return err
	}

	// A cache bypass picks up external changes (the refresh action, or the
	// daemon noticing a watched env file change).
	harvested, err := HarvestEnv(cfg.EnvCommand, filepath.Join(stateDir, "env.cache"), cfg.EnvTTL, bypassEnvCache)
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

	sock := sessionSocketPath()
	snap, err := FetchSnapshot(sock)
	if err != nil {
		return err
	}

	session := envOr("HERDR_SESSION", "default")

	// Tabs first, so the window title's ${tab} sees the fresh label. The
	// reset action re-adopts the invoking tab regardless of its opt-out.
	if withTabs && cfg.Tabs.Enabled {
		forceTab := ""
		if event == "reset" {
			forceTab = envOr("HERDR_TAB_ID", contextTabID())
			if forceTab == "" {
				forceTab = snap.FocusedTabID
			}
		}
		statePath := tabStatePath(stateDir, session)
		tabStates := LoadTabStates(statePath)
		ReconcileTabs(sock, snap, cfg.Tabs, tabStates, forceTab)
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
	_, err = ApplyTitle(sock, filepath.Join(stateDir, "last_title."+session), title)
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

// hookLingerQuiet is how long the daemonless hook waits after the most
// recent event for its pane before considering the stream quiet.
// hookLingerMax caps the whole linger so a hook process for a spammy
// pane cannot live forever.
const (
	hookLingerQuiet = 300 * time.Millisecond
	hookLingerMax   = 2 * time.Second
)

// lingerPaneTitles watches pane.updated events for one pane on an
// established subscription until this pane's events stay quiet (or the
// cap expires), then calls apply once if anything changed. Events act
// as a change signal only: the applier re-reads the pane under the lock,
// so racing hooks are ordered by lock acquisition over current server state,
// never by replaying possibly-stale event payloads.
func lingerPaneTitles(conn net.Conn, reader *bufio.Reader, paneID string, quiet, max time.Duration,
	apply func() error) error {
	lingerEnd := time.Now().Add(max)
	quietEnd := time.Now().Add(quiet)
	pending := false
	for {
		deadline := quietEnd
		if lingerEnd.Before(deadline) {
			deadline = lingerEnd
		}
		_ = conn.SetReadDeadline(deadline)
		line, err := reader.ReadString('\n')
		if err != nil {
			// Quiet period elapsed, cap expired, or the stream ended
			break
		}
		var ev struct {
			Event string `json:"event"`
			Data  struct {
				Pane *struct {
					PaneID string `json:"pane_id"`
				} `json:"pane"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Event != "pane_updated" {
			continue
		}
		if ev.Data.Pane == nil || ev.Data.Pane.PaneID != paneID {
			continue
		}
		pending = true
		quietEnd = time.Now().Add(quiet)
	}
	if !pending {
		return nil
	}
	return apply()
}

// paneHasTitle reports whether a pane currently carries a terminal title. A
// pane.get blip counts as titled: when in doubt the hook must yield rather
// than risk a process-derived rename over a title the daemon owns.
func paneHasTitle(sockPath, paneID string) bool {
	if paneID == "" {
		return true
	}
	p, ok := paneInfo(sockPath, paneID)
	if !ok {
		return true
	}
	return p.Title != ""
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

	// When using terminal titles, a process-derived rename would only fight
	// the title the shell is about to set: the daemon's pane.updated stream
	// owns titled panes, so the hook just revives it when dead. But the
	// daemon only reacts to events, and herdr has no "foreground command
	// changed" event, so a program that sets NO title (helix, less, most CLI
	// tools) is invisible to it: on an untitled pane the hook is the only
	// thing that can name the tab, and it falls through to the normal
	// process-derived rename below. With the daemon disabled by
	// watch_titles=false, the hook keeps just the focused tab up to date, and
	// leaves the rest to the watchdog events' full passes.
	if tabs.TerminalTitles {
		stateDir := pluginStateDir()
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			return err
		}
		session := envOr("HERDR_SESSION", "default")
		if tabs.WatchTitles {
			ensureDaemon(stateDir, session)
			if paneHasTitle(sessionSocketPath(), os.Getenv("HERDR_PANE_ID")) {
				return nil // the daemon owns titled panes
			}
			// Untitled pane: fall through to the process-derived rename.
		} else {
			// Subscribe before reading to ensure we don't miss any events. Events
			// that overlap the read are no-ops if they're the same as the current
			// label, and every lingering hook sees the same server-ordered stream,
			// so racing hooks converge on the newest title.
			sock := sessionSocketPath()
			paneID := os.Getenv("HERDR_PANE_ID")
			statePath := tabStatePath(stateDir, session)
			// apply reads pane info and renames under the lock, so racing hooks
			// always act on fresh state, and follows the fast path's rerun contract:
			// a contender hands its work to the holder, so a rerun must be a superset
			// of whatever the contender wanted — another hook's fresher title or a
			// watchdog's full reconcile.
			apply := func() error {
				first := true
				return withLock(stateDir, session, func() error {
					if !first {
						return pass("rerun", true, false)
					}
					first = false
					p, ok := paneInfo(sock, paneID)
					if !ok {
						// Don't need to escalate here, the next hook event retries.
						return nil
					}
					if p.TabID != tabID {
						// The pane moved out of this tab mid-linger, its title no
						// longer names this tab, and its new tab is not ours to touch.
						return nil
					}
					retryFull, err := RenameTabForTitle(sock, statePath,
						tabID, paneID, p.Agent, p.Title, p.Focused, tabs)
					if err != nil {
						return err
					}
					if retryFull {
						// A transient tab.get/process-info blip prevented a rename
						// that was due; the linger exits quiet and nothing resends
						// this title, so escalate to a full pass like the daemon does.
						return pass("rerun", true, false)
					}
					return nil
				})
			}
			if mode == "preexec" {
				// Shells set the title at the prompt, so preexec sees the previous
				// prompt's title: applying it is a no-op at best, and lingering here
				// would only double the subscriber processes per command — precmd's
				// linger covers the same window.
				return apply()
			}
			conn, reader, subErr := subscribeEvents(sock, []string{"pane.updated"}, hookLingerMax)
			if subErr == nil {
				defer conn.Close()
			}
			if err := apply(); err != nil {
				return err
			}
			if subErr != nil {
				// Lingering is best-effort, so just exit if we can't subscribe.
				return nil
			}
			return lingerPaneTitles(conn, reader, paneID, hookLingerQuiet, hookLingerMax, apply)
		}
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

	// Every shell prompt is a chance to revive a dead watch daemon.
	ensureDaemon(stateDir, envOr("HERDR_SESSION", "default"))

	// First pass under the lock is the single-tab fast rename; any rerun flag
	// raised meanwhile escalates to a full reconcile, which is a superset —
	// a structural event that raced this hook still gets handled.
	first := true
	return withLock(stateDir, envOr("HERDR_SESSION", "default"), func() error {
		if !first {
			return pass("rerun", true, false)
		}
		first = false

		sock := sessionSocketPath()
		if sampled {
			p, c, err := paneProgram(sock, os.Getenv("HERDR_PANE_ID"))
			if err != nil || p == "" {
				return nil // never guess
			}
			prog, cmdline = p, c
		}

		name := FormatTabName(prog, cmdline, tabs)
		if name == "" && !tabs.HideShell {
			return nil
		}
		label, ok := tabLabel(sock, tabID)
		if !ok {
			return nil
		}

		statePath := tabStatePath(stateDir, envOr("HERDR_SESSION", "default"))
		states := LoadTabStates(statePath)
		if !states.Eligible(tabID, label, name, false) {
			return SaveTabStates(statePath, states) // Eligible may record an opt-out
		}
		if name != label {
			renameTab(sock, tabID, name)
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
	case "init-config":
		err = runInit()
	case "init":
		err = runShellInit(os.Args[2:])
	case "refresh-all":
		err = runRefreshAll()
	case "watch":
		if len(os.Args) > 2 && os.Args[2] == "--detached" {
			err = runWatchDetached()
		} else {
			err = runWatchParent()
		}
	case "workspace.focused", "tab.focused":
		// Watchdog hooks: a live daemon already handles the event; a dead
		// one is revived and the gap covered.
		err = runWatchdog(event)
	case "pane.agent_status_changed":
		err = runStatusHook(event)
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
