# herdr-window-title

A [herdr](https://herdr.dev) plugin that sets the terminal window title from an
HCL template, combining live herdr state with the user's shell environment:

```
mysession : Work @ HQ › myproject › 1 › ×2 ✓1
└─ base name ┘ └ env ─┘  └ space ┘ └tab┘ └ attn ┘
```

- **space / tab** — the currently focused herdr workspace and tab labels.
- **attention** — how many agents need you, with a status icon per state
  (`blocked`, `done`, `unknown` by default), counted across all spaces.
- **environment** — anything your shell exports (e.g.
  [Overseer](https://overseer.olrik.dev/)'s `OVERSEER_*` context/location
  variables) is available in the template. The plugin spawns your login shell
  to harvest a fresh environment, cached briefly between event bursts.

## Install

```sh
herdr plugin link /path/to/herdr-window-title
```

The manifest's build step compiles the binary with `go build` (Go must be on
the herdr server's PATH). The plugin then reacts to workspace/tab focus and
rename events and to agent status changes; the title is only pushed when it
actually changed.

## Configure

Generate the fully-documented default config, then edit it:

```sh
herdr plugin action invoke davidolrik.window-title.init-config
$EDITOR "$(herdr plugin config-dir davidolrik.window-title)/config.hcl"
```

(The same file can be written with the binary directly: `herdr-window-title init`.
It never overwrites an existing config.hcl.)

Every setting is optional; the generated file documents the template variables,
functions, and defaults. Without a config file the default template mirrors
`"<name> : <Context> @ <Location>"` (the pre-plugin title) and appends space,
tab, and attention.

## Refresh on external changes

Herdr events do not fire when only the environment changes (e.g. an Overseer
context switch). Trigger a re-render with a fresh environment via:

```sh
herdr plugin action invoke davidolrik.window-title.refresh
```

For Overseer, a context-change hook can be reduced to exactly that command.

## Develop

```sh
go test ./...
```

The end-to-end test builds the real binary and drives it against a fake
`herdr` script, so no live herdr session is touched.
