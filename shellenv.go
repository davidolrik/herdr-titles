package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// shellTimeout bounds the env-harvesting shell spawn; a login zsh with a
// heavy profile chain takes well under a second, so anything longer is a hang.
const shellTimeout = 15 * time.Second

// ParseEnvNul parses NUL-separated KEY=VALUE entries as produced by `env -0`.
// Values may contain newlines; entries without an equals sign are skipped.
func ParseEnvNul(data []byte) map[string]string {
	env := map[string]string{}
	for _, entry := range bytes.Split(data, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		key, value, ok := bytes.Cut(entry, []byte{'='})
		if !ok || len(key) == 0 {
			continue
		}
		env[string(key)] = string(value)
	}
	return env
}

// cleanEnv builds the minimal environment for the harvest shell. The plugin
// inherits the herdr server's environment, which is a stale snapshot from
// server start; passing it through would defeat the harvest (e.g. zsh setups
// that skip re-sourcing when their variables look already set). Starting from
// an allowlist forces the shell's startup files to rebuild everything, exactly
// like a fresh terminal.
func cleanEnv() []string {
	keep := []string{"HOME", "USER", "LOGNAME", "SHELL", "TERM", "TMPDIR", "LANG"}
	env := []string{"PATH=/usr/bin:/bin"}
	for _, key := range keep {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "LC_") {
			env = append(env, entry)
		}
	}
	return env
}

// harvestCommand builds the harvest shell's exec.Cmd: the scrubbed
// environment, no stdin, and its OWN SESSION. The default command is a
// non-interactive login shell, which never touches a terminal; but the
// command is user-configurable, and an INTERACTIVE zsh that can open its
// controlling terminal makes itself that terminal's foreground process group.
// Spawned from a shell hook, whose controlling terminal is the user's pane,
// that stole the tty from the user's foreground command, which was then
// stopped with SIGTTOU on its next tcsetattr ("zsh: suspended (tty output)
// brew upgrade"). A new session has no controlling terminal, so there is
// nothing to grab whatever the command; the daemon and herdr's own hook
// spawns already run that way.
func harvestCommand(ctx context.Context, command []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env = cleanEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// HarvestEnv returns the environment exported by running command (expected to
// emit `env -0` style output). The raw output is cached at cachePath; a cache
// younger than ttl is reused so bursts of herdr events don't each spawn a
// shell. bypassCache forces a fresh spawn and rewrites the cache.
func HarvestEnv(command []string, cachePath string, ttl time.Duration, bypassCache bool) (map[string]string, error) {
	if !bypassCache {
		if info, err := os.Stat(cachePath); err == nil && time.Since(info.ModTime()) < ttl {
			if data, err := os.ReadFile(cachePath); err == nil {
				return ParseEnvNul(data), nil
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
	defer cancel()
	cmd := harvestCommand(ctx, command)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("env command %v: %w", command, err)
	}

	// Best-effort cache write; harvesting still succeeds without it.
	_ = os.WriteFile(cachePath, stdout.Bytes(), 0o600)

	return ParseEnvNul(stdout.Bytes()), nil
}
