package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"

	"github.com/hashicorp/hcl/v2"
)

// standardStatuses are herdr's agent lifecycle states; counts always carries
// all of them so templates can reference e.g. counts.working without guards.
var standardStatuses = []string{"idle", "working", "blocked", "done", "unknown"}

// scopedCounts tallies agents per status, honoring the configured scope.
func scopedCounts(agents []Agent, cfg *Config, focusedWorkspaceID string) map[string]int {
	counts := map[string]int{}
	for _, s := range standardStatuses {
		counts[s] = 0
	}
	for _, a := range agents {
		if cfg.Scope == "focused-workspace" && a.WorkspaceID != focusedWorkspaceID {
			continue
		}
		counts[a.Status]++
	}
	return counts
}

// RenderAttention renders the "needs attention" summary, e.g. "✋2 ✅1".
// Statuses appear in configured order; zero counts are omitted; a status with
// no configured icon renders as "<status>:<count>".
func RenderAttention(agents []Agent, cfg *Config, focusedWorkspaceID string) string {
	counts := scopedCounts(agents, cfg, focusedWorkspaceID)
	var parts []string
	for _, status := range cfg.Statuses {
		n := counts[status]
		if n == 0 {
			continue
		}
		if icon := cfg.Icons[status]; icon != "" {
			parts = append(parts, fmt.Sprintf("%s%d", PadIcons(icon), n))
		} else {
			parts = append(parts, fmt.Sprintf("%s:%d", status, n))
		}
	}
	return strings.Join(parts, " ")
}

// PadIcons inserts an extra space after every private-use-area rune (nerd-font
// icons). Terminal sidebars render these glyphs in a patched monospace font,
// but the window title bar uses the system font, where they overflow their
// cell and smush into the following character.
func PadIcons(s string) string {
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(r)
		if (r >= 0xE000 && r <= 0xF8FF) || (r >= 0xF0000 && r <= 0xFFFFD) || (r >= 0x100000 && r <= 0x10FFFD) {
			b.WriteRune(' ')
		}
	}
	return b.String()
}

// expandTilde resolves a leading "~/" against the current home directory.
func expandTilde(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path[1:], "/"))
		}
	}
	return path
}

var fileFunc = function.New(&function.Spec{
	Params: []function.Parameter{{Name: "path", Type: cty.String}},
	Type:   function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
		data, err := os.ReadFile(expandTilde(args[0].AsString()))
		if err != nil {
			return cty.StringVal(""), nil
		}
		return cty.StringVal(strings.TrimSpace(string(data))), nil
	},
})

// coalesceFunc returns the first argument that is neither null nor empty.
var coalesceFunc = function.New(&function.Spec{
	VarParam: &function.Parameter{Name: "values", Type: cty.String, AllowNull: true},
	Type:     function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
		for _, v := range args {
			if !v.IsNull() && v.AsString() != "" {
				return v, nil
			}
		}
		return cty.StringVal(""), nil
	},
})

// getenvFunc looks up a harvested environment variable, "" when absent —
// unlike env.NAME attribute access, which fails hard on a missing variable.
func getenvFunc(env map[string]string) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "name", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			return cty.StringVal(env[args[0].AsString()]), nil
		},
	})
}

// ComposeTitle evaluates the configured template against the current herdr
// snapshot, session name, and harvested environment.
func ComposeTitle(cfg *Config, snap *Snapshot, session string, env map[string]string) (string, error) {
	counts := scopedCounts(snap.Agents, cfg, snap.FocusedWorkspaceID)
	countVals := map[string]cty.Value{}
	for status, n := range counts {
		countVals[status] = cty.NumberIntVal(int64(n))
	}

	envVals := map[string]cty.Value{}
	for k, v := range env {
		envVals[k] = cty.StringVal(v)
	}

	ctx := &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"workspace": cty.StringVal(snap.WorkspaceLabel),
			"tab":       cty.StringVal(snap.TabLabel),
			"session":   cty.StringVal(session),
			"attention": cty.StringVal(RenderAttention(snap.Agents, cfg, snap.FocusedWorkspaceID)),
			"counts":    cty.ObjectVal(countVals),
			"env":       cty.ObjectVal(envVals),
		},
		Functions: map[string]function.Function{
			"file":     fileFunc,
			"coalesce": coalesceFunc,
			"getenv":   getenvFunc(env),
			"format":   stdlib.FormatFunc,
			"pad_icons": function.New(&function.Spec{
				Params: []function.Parameter{{Name: "text", Type: cty.String}},
				Type:   function.StaticReturnType(cty.String),
				Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
					return cty.StringVal(PadIcons(args[0].AsString())), nil
				},
			}),
		},
	}

	value, diags := cfg.Template.Value(ctx)
	if diags.HasErrors() {
		return "", fmt.Errorf("evaluate template: %w", diags)
	}
	value, err := convert.Convert(value, cty.String)
	if err != nil {
		return "", fmt.Errorf("template result is not a string: %w", err)
	}
	return value.AsString(), nil
}
