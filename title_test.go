package main

import (
	"os"
	"path/filepath"
	"testing"
)

func testConfig(t *testing.T, hclSrc string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.hcl")
	if err := os.WriteFile(path, []byte(hclSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

func attentionConfig(t *testing.T, scope string) *Config {
	return testConfig(t, `
template = "unused"
attention {
  statuses = ["blocked", "done", "unknown"]
  scope    = "`+scope+`"
  icons = {
    blocked = "B"
    done    = "D"
    unknown = "U"
  }
}
`)
}

func TestRenderAttention(t *testing.T) {
	cfg := attentionConfig(t, "all")
	agents := []Agent{
		{Status: "blocked", WorkspaceID: "w1"},
		{Status: "blocked", WorkspaceID: "w2"},
		{Status: "done", WorkspaceID: "w1"},
		{Status: "working", WorkspaceID: "w1"},
		{Status: "idle", WorkspaceID: "w3"},
	}
	got := RenderAttention(agents, cfg, "w1")
	if got != "B2 D1" {
		t.Errorf("RenderAttention = %q, want %q", got, "B2 D1")
	}
}

func TestRenderAttentionEmpty(t *testing.T) {
	cfg := attentionConfig(t, "all")
	agents := []Agent{{Status: "working", WorkspaceID: "w1"}, {Status: "idle", WorkspaceID: "w2"}}
	if got := RenderAttention(agents, cfg, "w1"); got != "" {
		t.Errorf("RenderAttention = %q, want empty", got)
	}
}

func TestRenderAttentionFocusedSpaceScope(t *testing.T) {
	cfg := attentionConfig(t, "focused-workspace")
	agents := []Agent{
		{Status: "blocked", WorkspaceID: "w1"},
		{Status: "blocked", WorkspaceID: "w2"},
	}
	if got := RenderAttention(agents, cfg, "w1"); got != "B1" {
		t.Errorf("RenderAttention = %q, want B1", got)
	}
}

func TestRenderAttentionMissingIconFallsBackToStatusName(t *testing.T) {
	cfg := testConfig(t, `
template = "unused"
attention {
  statuses = ["blocked"]
  icons    = { done = "D" }
}
`)
	agents := []Agent{{Status: "blocked", WorkspaceID: "w1"}}
	if got := RenderAttention(agents, cfg, "w1"); got != "blocked:1" {
		t.Errorf("RenderAttention = %q, want blocked:1", got)
	}
}

func TestPadIcons(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"nerd font plane-15 icon", "\U000F06A9 claude", "\U000F06A9  claude"},
		{"bmp private-use icon", " branch", "  branch"},
		{"icon glued to text", "\U000F06A9claude", "\U000F06A9 claude"},
		{"plain text untouched", "zsh", "zsh"},
		{"emoji untouched", "✋2 ✅1", "✋2 ✅1"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PadIcons(tc.in); got != tc.want {
				t.Errorf("PadIcons(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestComposeTitlePadIconsFunction(t *testing.T) {
	cfg := testConfig(t, `template = "${pad_icons(tab)}"`)
	snap := composeSnap()
	snap.TabLabel = "\U000F06A9 claude"
	got, err := ComposeTitle(cfg, snap, "s", map[string]string{})
	if err != nil {
		t.Fatalf("ComposeTitle: %v", err)
	}
	if got != "\U000F06A9  claude" {
		t.Errorf("ComposeTitle = %q, want padded icon", got)
	}
}

func TestRenderAttentionPadsPrivateUseIcons(t *testing.T) {
	cfg := testConfig(t, `
template = "unused"
attention {
  statuses = ["blocked"]
  icons    = { blocked = "\U000F06A9" }
}
`)
	agents := []Agent{{Status: "blocked", WorkspaceID: "w1"}}
	if got := RenderAttention(agents, cfg, "w1"); got != "\U000F06A9 1" {
		t.Errorf("RenderAttention = %q, want icon padded before count", got)
	}
}

func composeSnap() *Snapshot {
	return &Snapshot{
		FocusedWorkspaceID: "w1",
		WorkspaceLabel:     "myspace",
		TabLabel:           "mytab",
		Agents: []Agent{
			{Status: "blocked", WorkspaceID: "w1"},
			{Status: "working", WorkspaceID: "w2"},
		},
	}
}

func TestComposeTitleVariables(t *testing.T) {
	cfg := testConfig(t, `
template = "${session}/${workspace}/${tab}/${attention}/${counts.working}"
attention { icons = { blocked = "B" } }
`)
	got, err := ComposeTitle(cfg, composeSnap(), "mysession", map[string]string{})
	if err != nil {
		t.Fatalf("ComposeTitle: %v", err)
	}
	if got != "mysession/myspace/mytab/B1/1" {
		t.Errorf("ComposeTitle = %q", got)
	}
}

func TestComposeTitleEnvAccess(t *testing.T) {
	cfg := testConfig(t, `template = "${env.MY_VAR}|${getenv("MY_VAR")}|${getenv("MISSING")}"`)
	got, err := ComposeTitle(cfg, composeSnap(), "s", map[string]string{"MY_VAR": "hello"})
	if err != nil {
		t.Fatalf("ComposeTitle: %v", err)
	}
	if got != "hello|hello|" {
		t.Errorf("ComposeTitle = %q, want %q", got, "hello|hello|")
	}
}

func TestComposeTitleFileAndCoalesce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "override"), []byte("Custom Name\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t, `template = "${coalesce(file("~/override"), session)}+${coalesce(file("~/missing"), session)}"`)
	got, err := ComposeTitle(cfg, composeSnap(), "fallback", map[string]string{})
	if err != nil {
		t.Fatalf("ComposeTitle: %v", err)
	}
	if got != "Custom Name+fallback" {
		t.Errorf("ComposeTitle = %q, want %q", got, "Custom Name+fallback")
	}
}

func TestComposeTitleTernary(t *testing.T) {
	cfg := testConfig(t, `
template = "x${attention != "" ? " · ${attention}" : ""}"
attention { icons = { blocked = "B" } }
`)

	snap := composeSnap()
	got, err := ComposeTitle(cfg, snap, "s", map[string]string{})
	if err != nil {
		t.Fatalf("ComposeTitle: %v", err)
	}
	if got != "x · B1" {
		t.Errorf("ComposeTitle = %q, want %q", got, "x · B1")
	}

	snap.Agents = nil
	got, err = ComposeTitle(cfg, snap, "s", map[string]string{})
	if err != nil {
		t.Fatalf("ComposeTitle: %v", err)
	}
	if got != "x" {
		t.Errorf("ComposeTitle = %q, want %q", got, "x")
	}
}

func TestComposeTitleDefaultTemplateWithoutOverseer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/zsh")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ComposeTitle(cfg, composeSnap(), "main", map[string]string{})
	if err != nil {
		t.Fatalf("ComposeTitle with default template: %v", err)
	}
	want := "main › myspace › mytab › ×1"
	if got != want {
		t.Errorf("ComposeTitle = %q, want %q", got, want)
	}
}

func TestComposeTitleDefaultTemplateWithOverseer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/zsh")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"OVERSEER_CONTEXT_DISPLAY_NAME":  "Client",
		"OVERSEER_LOCATION_DISPLAY_NAME": "Andel",
	}
	got, err := ComposeTitle(cfg, composeSnap(), "main", env)
	if err != nil {
		t.Fatalf("ComposeTitle with default template: %v", err)
	}
	want := "main : Client @ Andel › myspace › mytab › ×1"
	if got != want {
		t.Errorf("ComposeTitle = %q, want %q", got, want)
	}
}
