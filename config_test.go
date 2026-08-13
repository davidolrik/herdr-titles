package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.hcl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigFull(t *testing.T) {
	path := writeConfig(t, `
template = "${workspace} - ${tab}"

env {
  command = ["/bin/sh", "-c", "printf 'A=b\\0'"]
  ttl     = "30s"
}

attention {
  statuses = ["blocked"]
  scope    = "focused-workspace"
  icons = {
    blocked = "!"
  }
}
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Template == nil {
		t.Error("Template expression is nil")
	}
	if got := cfg.EnvCommand[0]; got != "/bin/sh" {
		t.Errorf("EnvCommand[0] = %q, want /bin/sh", got)
	}
	if cfg.EnvTTL != 30*time.Second {
		t.Errorf("EnvTTL = %v, want 30s", cfg.EnvTTL)
	}
	if len(cfg.Statuses) != 1 || cfg.Statuses[0] != "blocked" {
		t.Errorf("Statuses = %v, want [blocked]", cfg.Statuses)
	}
	if cfg.Scope != "focused-workspace" {
		t.Errorf("Scope = %q, want focused-workspace", cfg.Scope)
	}
	if cfg.Icons["blocked"] != "!" {
		t.Errorf("Icons[blocked] = %q, want !", cfg.Icons["blocked"])
	}
}

func TestLoadConfigMissingFileUsesDefaults(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.hcl"))
	if err != nil {
		t.Fatalf("LoadConfig on missing file: %v", err)
	}
	if cfg.Template == nil {
		t.Error("default Template expression is nil")
	}
	if len(cfg.EnvCommand) == 0 || cfg.EnvCommand[0] != "/bin/zsh" {
		t.Errorf("default EnvCommand = %v, want to start with $SHELL (/bin/zsh)", cfg.EnvCommand)
	}
	if cfg.EnvTTL != 10*time.Second {
		t.Errorf("default EnvTTL = %v, want 10s", cfg.EnvTTL)
	}
	want := []string{"blocked", "done", "unknown"}
	if len(cfg.Statuses) != len(want) {
		t.Fatalf("default Statuses = %v, want %v", cfg.Statuses, want)
	}
	for i, s := range want {
		if cfg.Statuses[i] != s {
			t.Errorf("default Statuses[%d] = %q, want %q", i, cfg.Statuses[i], s)
		}
	}
	if cfg.Scope != "all" {
		t.Errorf("default Scope = %q, want all", cfg.Scope)
	}
	// All herdr states carry an icon so enabling one is a statuses-only edit.
	wantIcons := map[string]string{
		"idle": "○", "working": "◐", "blocked": "×", "done": "✓", "unknown": "·",
	}
	for s, icon := range wantIcons {
		if cfg.Icons[s] != icon {
			t.Errorf("default Icons[%q] = %q, want %q", s, cfg.Icons[s], icon)
		}
	}
}

func TestLoadConfigPartialKeepsOtherDefaults(t *testing.T) {
	path := writeConfig(t, `template = "${workspace}"`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Scope != "all" {
		t.Errorf("Scope = %q, want default all", cfg.Scope)
	}
	if cfg.EnvTTL != 10*time.Second {
		t.Errorf("EnvTTL = %v, want default 10s", cfg.EnvTTL)
	}
}

func TestLoadConfigInvalidHCL(t *testing.T) {
	path := writeConfig(t, `template = `)
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "config.hcl") {
		t.Errorf("error %q does not mention the file name", err)
	}
}

func TestLoadConfigInvalidTTL(t *testing.T) {
	path := writeConfig(t, `
template = "x"
env { ttl = "not-a-duration" }
`)
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected ttl parse error, got nil")
	}
}

func TestLoadConfigInvalidScope(t *testing.T) {
	path := writeConfig(t, `
template = "x"
attention { scope = "everywhere" }
`)
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected scope validation error, got nil")
	}
}

func TestWriteDefaultConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "config")
	path, err := WriteDefaultConfig(dir)
	if err != nil {
		t.Fatalf("WriteDefaultConfig: %v", err)
	}
	if path != filepath.Join(dir, "config.hcl") {
		t.Errorf("path = %q, want config.hcl inside %q", path, dir)
	}

	// The generated file is the documented example config verbatim.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	example, err := os.ReadFile("config.example.hcl")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(example) {
		t.Error("generated config differs from config.example.hcl")
	}

	// It must parse, and its template must match the built-in default.
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
	if cfg.Scope != "all" || cfg.Icons["blocked"] != "×" {
		t.Errorf("generated config parsed unexpectedly: scope=%q icons=%v", cfg.Scope, cfg.Icons)
	}
	if len(cfg.Icons) != 5 || cfg.Icons["working"] != "◐" || cfg.Icons["idle"] != "○" {
		t.Errorf("generated config icons = %v, want all five herdr state icons", cfg.Icons)
	}
	if got := len(cfg.Statuses); got != 3 {
		t.Errorf("generated config enables %d statuses, want 3", got)
	}

	// A second run must refuse to overwrite.
	if _, err := WriteDefaultConfig(dir); err == nil {
		t.Fatal("expected error when config.hcl already exists, got nil")
	}
}

func TestGeneratedConfigTemplateMatchesBuiltInDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/zsh")
	dir := t.TempDir()
	path, err := WriteDefaultConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	builtin, err := LoadConfig(filepath.Join(dir, "missing.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	snap := &Snapshot{FocusedWorkspaceID: "w1", WorkspaceLabel: "sp", TabLabel: "tb",
		Agents: []Agent{{Status: "blocked", WorkspaceID: "w1"}}}
	env := map[string]string{"OVERSEER_CONTEXT_DISPLAY_NAME": "Ctx", "OVERSEER_LOCATION_DISPLAY_NAME": "Loc"}
	a, err := ComposeTitle(generated, snap, "s", env)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ComposeTitle(builtin, snap, "s", env)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("generated config renders %q, built-in default renders %q", a, b)
	}
}

func TestExampleConfigTabsMatchBuiltInDefaults(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	dir := t.TempDir()
	path, err := WriteDefaultConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	builtin, err := LoadConfig(filepath.Join(dir, "missing.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(generated.Tabs, builtin.Tabs) {
		t.Errorf("example config tabs drift from built-in defaults:\nexample: %+v\nbuiltin: %+v", generated.Tabs, builtin.Tabs)
	}
}

func TestLoadConfigTerminalTitles(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tabs.TerminalTitles {
		t.Error("TerminalTitles default = true, want false")
	}

	path := writeConfig(t, `
template = "x"
tabs {
  terminal_titles = true
}
`)
	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Tabs.TerminalTitles {
		t.Error("terminal_titles = true not honored")
	}
}

func TestLoadConfigWatchKnobs(t *testing.T) {
	t.Setenv("HOME", "/home/probe")
	// Defaults: watching titles on, no env files watched.
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Tabs.WatchTitles {
		t.Error("WatchTitles default = false, want true")
	}
	if len(cfg.EnvWatchFiles) != 0 {
		t.Errorf("EnvWatchFiles default = %v, want empty", cfg.EnvWatchFiles)
	}

	path := writeConfig(t, `
template = "x"
env {
  watch_files = ["~/.local/var/overseer.env", "/etc/motd"]
}
tabs {
  watch_titles = false
}
`)
	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tabs.WatchTitles {
		t.Error("watch_titles = false not honored")
	}
	want := []string{"/home/probe/.local/var/overseer.env", "/etc/motd"}
	if len(cfg.EnvWatchFiles) != 2 || cfg.EnvWatchFiles[0] != want[0] || cfg.EnvWatchFiles[1] != want[1] {
		t.Errorf("EnvWatchFiles = %v, want %v (tilde expanded)", cfg.EnvWatchFiles, want)
	}
}
