# herdr-titles

A [herdr](https://herdr.dev) plugin that sets the terminal window title from an
HCL template, combining live herdr state with the user's shell environment:

```text
mysession : Work @ HQ › myproject › 1 › ×2 ✓1
└─ base name ┘ └ env ─┘  └ space ┘ └tab┘ └ attn ┘
```

- **space / tab** — the currently focused herdr workspace and tab labels.
- **attention** — how many agents need you, with a status icon per state
  (`blocked`, `done`, `unknown` by default), counted across all spaces.
- **tab naming** — tabs are automatically named after their foreground
  program (with optional Nerd Font icons), or after a hosted agent's session
  title; a manual rename opts that tab out. Ported from
  [qu8n/herdr-automatic-rename](https://github.com/qu8n/herdr-automatic-rename)
  (MIT), minus the jump-key numbering. For real-time per-command renames,
  source the matching hook from your shell startup:

  ```sh
  source /path/to/herdr-titles/shell/hook.zsh   # or .bash / .fish
  ```

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

The plugin reacts to workspace/tab focus and rename events and to agent status
changes; the title is only pushed when it actually changed.

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
`"<name> : <Context> @ <Location>"` (the pre-plugin title) and appends space,
tab, and attention.

## Refresh on external changes

Herdr events do not fire when only the environment changes (e.g. an Overseer
context switch). Trigger a re-render with a fresh environment via:

```sh
herdr plugin action invoke davidolrik.titles.refresh
```

For Overseer, a context-change hook can be reduced to exactly that command.

## Develop

```sh
go test ./...
```

The end-to-end test builds the real binary and drives it against a fake
`herdr` script, so no live herdr session is touched.
