# herdr-titles

Titles that keep up. A [herdr](https://herdr.dev) plugin that keeps every
title in sync with what's actually happening — the terminal window title from
an HCL template, and every tab named after what runs inside it:

```text
mysession : Work @ HQ › myproject › 1 › ×2 ✓1
└─ base name ┘ └ env ─┘ └workspace┘ └tab┘ └ attn ┘
```

## Features

- **Window title from an HCL template** — compose it from the focused
  `workspace` and `tab` labels, the herdr `session` name, per-status agent
  `counts`, an `attention` summary, and anything in your environment (`env`,
  `getenv()`), with helper functions (`file`, `coalesce`, `format`,
  `pad_icons`).
- **Agent attention counts** — see how many agents need you from any window,
  with herdr's own status symbols (`×` blocked, `✓` done, `·` unknown by
  default; every state configurable).
- **Automatic tab naming** — tabs follow their foreground program, with
  optional Nerd Font icons, aliases, regex substitutions, and a
  `hide_shell` mode. Icons stay in the tab bar but are stripped from the
  window title on macOS, whose title bar can't render them
  (`titlebar_icons`). Script interpreters are unwrapped to the tool they
  run — `ansible-playbook` shows as `ansible-playbook`, not `python`.
  Ported from
  [qu8n/herdr-automatic-rename](https://github.com/qu8n/herdr-automatic-rename)
  (MIT), minus the jump-key numbering.
- **Terminal titles** — with `tabs { terminal_titles = true }`, a pane whose
  shell (or program) sets a terminal title names its tab after that title.
  The foreground program name remains the fallback (and the default). In a
  background multi-pane tab, the name follows the last-focused pane, so it
  remains consistent when you switch to that tab again.
- **Live agent session titles** — a tab hosting a coding agent is named
  after the agent's session title instead of its terminal title or process name
  (`󰚩 Fix flaky integration test`, not `󰚩 claude`), and follows renames —
  Claude's `/rename` shows up in the tab within a second.
- **Real-time shell hooks** — zsh, bash, and fish hooks rename the tab the
  moment you run a command, not when herdr happens to notice.
- **Environment aware** — the plugin harvests your login shell's environment
  (so tools like [Overseer](https://overseer.olrik.dev/) Just Work), caches
  it briefly, and can watch files for changes
  (`env { watch_files = [...] }`) so context switches appear by themselves.
- **A self-healing per-session daemon** — subscribes to herdr's event stream
  (including events plugin hooks can't receive) and drives all updates with
  debounced, scoped passes: near-zero CPU even in busy sessions. If it dies,
  the retained watchdog hooks and every shell prompt revive it.
- **Manual control when you want it** — rename a tab by hand and the plugin
  leaves it alone permanently; hand it back by renaming it to a space or a
  bare number, or with the `reset` action.
- **Painless install** — prebuilt, checksum-verified binaries; only `sh` and
  `curl` needed, never Go.

## Install

```sh
herdr plugin install davidolrik/herdr-titles
```

The install step downloads the prebuilt, checksum-verified binary for your
platform from this repo's releases — only `sh` and `curl` are needed, not Go.
When no release asset matches (a dev checkout, an unusual platform, offline),
it falls back to `go build`.

For development, link a local checkout instead:

```sh
herdr plugin link /path/to/herdr-titles
go build -o bin/herdr-titles .   # `plugin link` does not run the build step
```

## Configure

Generate the fully-documented default config, then edit it:

```sh
herdr plugin action invoke davidolrik.titles.init-config
$EDITOR "$(herdr plugin config-dir davidolrik.titles)/config.hcl"
```

(The same file can be written with the binary directly: `herdr-titles init`.
It never overwrites an existing config.hcl.)

Every setting is optional; the generated file documents the template
variables, functions, and defaults. Without a config file the default
template mirrors `"<name> : <Context> @ <Location>"` and appends workspace,
tab, and attention.

## Actions

| Action                          | What it does                                            |
| ------------------------------- | ------------------------------------------------------- |
| `davidolrik.titles.refresh`     | Re-render now with a freshly harvested environment      |
| `davidolrik.titles.refresh-all` | The same, for every running session (also a subcommand) |
| `davidolrik.titles.reset`       | Re-adopt the invoking tab into automatic naming         |
| `davidolrik.titles.init-config` | Write the documented default config file                |

Invoke with `herdr plugin action invoke <action>`, or bind one to a key in
herdr's `config.toml` (`type = "plugin_action"`).

## Manual renames

A hand-renamed tab is yours: the plugin opts it out and never touches it
again. To hand it back to automatic naming, rename it to just a **space**
(herdr's rename UI rejects an empty name, but whitespace and bare numbers
both count as "cleared"), or run the `reset` action from that tab.

## Shell hook: real-time tab names

Herdr has no "foreground command changed" event, so sourcing the bundled
hook makes tabs follow every command the moment you run it.

`herdr plugin install` puts the plugin in a directory named after the plugin
id plus a hash (`davidolrik.titles-<hash>`). The hash is stable across
reinstalls, but a glob keeps your shell config independent of it:

```sh
# zsh (~/.zshrc or a conf.d file) — (N) makes a missing plugin a silent no-op
for _f in ${HOME}/.config/herdr/plugins/github/davidolrik.titles-*/shell/hook.zsh(N); do
  source $_f; break
done
```

```sh
# bash (~/.bashrc) — source AFTER prompt/history tools like starship or atuin
for _f in ${HOME}/.config/herdr/plugins/github/davidolrik.titles-*/shell/hook.bash; do
  [ -f "$_f" ] && { source "$_f"; break; }
done
```

```sh
# fish (~/.config/fish/config.fish)
for _f in ~/.config/herdr/plugins/github/davidolrik.titles-*/shell/hook.fish
    source $_f; break
end
```

The hooks are no-ops outside a herdr pane, background every call so the
prompt never blocks, and stay registered across re-sourcing. On bash they
cooperate with bash-preexec/ble.sh/atuin instead of clobbering the DEBUG
trap; see the comments in `shell/hook.bash`.

With `tabs { terminal_titles = true }`, you don't really need the hooks.
They become no-ops to avoid fighting with the terminal title your shell
is about to set. They will only revive a dead watch daemon if needed.

## The watch daemon

A small per-session daemon (started by the plugin's `[[startup]]` hook,
detached so it never occupies a plugin slot) subscribes to herdr's socket
event stream — including `pane.updated`, which herdr deliberately excludes
from plugin event hooks. That's what makes agent title changes appear
instantly and keeps the manifest down to three tiny watchdog hooks.

It is self-healing by construction: the daemon holds a per-session lock for
its lifetime, and the watchdog hooks (plus every shell prompt) probe that
lock and respawn a dead daemon. It also watches its own binary — installing
a plugin update (or rebuilding a dev checkout) makes every running daemon
exec itself onto the new binary within seconds, no restart required. Work is debounced and scoped — frequent
events take cheap title-only or targeted-rename paths with zero subprocess
spawns, and full reconciles are rate-limited — so steady-state CPU cost is
near zero. `tabs { watch_titles = false }` disables it; titles then update
on focus changes only.

## Using with Overseer (or any tool that changes your environment)

The template can render anything your shell exports. For example, with
[Overseer](https://overseer.olrik.dev/) exporting
`OVERSEER_CONTEXT_DISPLAY_NAME` (its location variable works the same way):

```hcl
template = "${session} : ${getenv("OVERSEER_CONTEXT_DISPLAY_NAME")} › ${workspace} › ${tab}"
```

Use `getenv("VAR")` rather than `env.VAR` when the variable might be absent —
`getenv` returns `""`, `env.VAR` fails the render.

Herdr events do not fire when only the environment changes. On the machine
where herdr runs, the simplest wiring is no wiring: list the file your tool
rewrites in `env { watch_files = ["~/.local/var/overseer.env"] }` and the
watch daemon picks changes up by itself within a few seconds.

For remote setups, or tools that can only run hooks, poke the plugin's
`refresh-all` subcommand — it talks to every running session's socket
directly, with no dependencies beyond the plugin binary itself:

```sh
#!/bin/sh
# Resolve the installed plugin binary and refresh every herdr session.
for bin in "$HOME"/.config/herdr/plugins/github/davidolrik.titles-*/bin/herdr-titles; do
  [ -x "$bin" ] && exec "$bin" refresh-all
done
exit 0
```

Stopped sessions' leftover sockets are skipped silently.

## Develop

```sh
go test ./...
```

The test suite drives the real binary against fake `herdr` scripts and fake
session sockets (for the daemon), so no live herdr session is touched.
