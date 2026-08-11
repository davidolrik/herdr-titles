# herdr-titles

A [herdr](https://herdr.dev) plugin that sets the terminal window title from an
HCL template, combining live herdr state with the user's shell environment:

```text
mysession : Work @ HQ › myproject › 1 › ×2 ✓1
└─ base name ┘ └ env ─┘ └workspace┘ └tab┘ └ attn ┘
```

- **workspace / tab** — the currently focused herdr workspace and tab labels.
- **attention** — how many agents need you, with a status icon per state
  (`blocked`, `done`, `unknown` by default), counted across all workspaces.
- **tab naming** — tabs are automatically named after their foreground
  program (with optional Nerd Font icons), or after a hosted agent's session
  title; a manual rename opts that tab out. Ported from
  [qu8n/herdr-automatic-rename](https://github.com/qu8n/herdr-automatic-rename)
  (MIT), minus the jump-key numbering. For real-time per-command renames,
  source the matching hook from your shell startup — see
  [Shell hook](#shell-hook-real-time-tab-names).
- **environment** — anything your shell exports (e.g.
  [Overseer](https://overseer.olrik.dev/)'s `OVERSEER_*` context/location
  variables) is available in the template. The plugin spawns your login shell
  to harvest a fresh environment, cached briefly between event bursts.

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

A small per-session daemon (started by the plugin, self-healing via watchdog
hooks) subscribes to herdr's event stream and drives all updates — including
agent title changes like Claude's `/rename`, which no plugin event hook can
deliver. Work is debounced and scoped so even busy sessions cost near-zero
CPU; titles are only pushed when they actually changed. It can also watch
your environment files directly (`env { watch_files = [...] }`), which makes
external refresh hooks unnecessary on the local machine.

## Configure

Generate the fully-documented default config, then edit it:

```sh
herdr plugin action invoke davidolrik.titles.init-config
$EDITOR "$(herdr plugin config-dir davidolrik.titles)/config.hcl"
```

(The same file can be written with the binary directly: `herdr-titles init`.
It never overwrites an existing config.hcl.)

Every setting is optional; the generated file documents the template variables,
functions, and defaults. Without a config file the default template mirrors
`"<name> : <Context> @ <Location>"` (the pre-plugin title) and appends workspace,
tab, and attention.

## Shell hook: real-time tab names

Herdr has no "foreground command changed" event, so event-driven renames only
happen when herdr itself sees a change. Sourcing the bundled hook makes the
tab follow every command you run, the moment you run it.

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

## Using with Overseer (or any tool that changes your environment)

The template can render anything your shell exports. The plugin harvests the
environment by spawning your login shell (so tools that inject variables via
shell startup Just Work), and caches it briefly between event bursts. For
example, with [Overseer](https://overseer.olrik.dev/) exporting
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

Alternatively, give the external tool a change hook that pokes the plugin's
`refresh` action (it bypasses the environment cache). From inside a herdr
pane one command suffices:

```sh
herdr plugin action invoke davidolrik.titles.refresh
```

From a daemon context (like Overseer's hooks) the `herdr` CLI can only reach
one session — use the plugin's `refresh-all` subcommand instead, which talks
to every running session's socket directly (no dependencies beyond the
plugin binary itself):

```sh
#!/bin/sh
# Resolve the installed plugin binary and refresh every herdr session.
for bin in "$HOME"/.config/herdr/plugins/github/davidolrik.titles-*/bin/herdr-titles; do
  [ -x "$bin" ] && exec "$bin" refresh-all
done
exit 0
```

Wire that script into the tool's on-change hook (for Overseer:
`context_hooks`/`location_hooks` in its config), and a context switch shows
up in every window title immediately. Stopped sessions' leftover sockets are
skipped silently.

## Develop

```sh
go test ./...
```

The end-to-end test builds the real binary and drives it against a fake
`herdr` script, so no live herdr session is touched.
