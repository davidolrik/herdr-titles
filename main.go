// herdr-window-title composes the terminal window title from a configurable
// HCL template fed with herdr state (focused space, focused tab, agent
// attention counts) and the user's shell environment, then pushes it through
// `herdr terminal title set`. Herdr spawns it once per subscribed event; every
// invocation is one idempotent reconcile.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

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

func run(event string) error {
	configDir := envOr("HERDR_PLUGIN_CONFIG_DIR", defaultDir(".config", "herdr-window-title"))
	stateDir := envOr("HERDR_PLUGIN_STATE_DIR", defaultDir(".local/state", "herdr-window-title"))
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}

	// Serialize concurrent event bursts; herdr may spawn several of us at once.
	lockFile, err := os.OpenFile(filepath.Join(stateDir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}

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
	title, err := ComposeTitle(cfg, snap, session, env)
	if err != nil {
		return err
	}

	// The state dir is shared by every herdr session's server, so the record
	// of the last pushed title must be kept per session.
	_, err = ApplyTitle(herdrBin, filepath.Join(stateDir, "last_title."+session), title)
	return err
}

// runInit writes the documented default config file and reports where.
func runInit() error {
	configDir := envOr("HERDR_PLUGIN_CONFIG_DIR", defaultDir(".config", "herdr-window-title"))
	path, err := WriteDefaultConfig(configDir)
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
	if event == "init" {
		err = runInit()
	} else {
		err = run(event)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "herdr-window-title (%s): %v\n", event, err)
		os.Exit(1)
	}
}
