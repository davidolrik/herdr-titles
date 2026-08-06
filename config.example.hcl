# herdr-window-title configuration.
#
# Location: $HERDR_PLUGIN_CONFIG_DIR/config.hcl
#   (run `herdr plugin config-dir davidolrik.window-title` to find the directory;
#    without the plugin engine it falls back to ~/.config/herdr-window-title/)
#
# Every value below shows the built-in default. Delete the file — or any part
# of it — to get the defaults back.

# The window title as an HCL template expression.
#
# Variables:
#   space      focused workspace label
#   tab        focused tab label
#   session    herdr session name ($HERDR_SESSION, "default" outside herdr)
#   attention  pre-rendered summary like "×2 ✓1" ("" when nothing needs attention)
#   counts     per-status agent counts, e.g. counts.blocked or counts.working
#   env        the harvested shell environment, e.g. env.OVERSEER_CONTEXT
#              (fails on a missing variable — use getenv() when unsure)
#
# Functions:
#   getenv(name)       env lookup that returns "" when the variable is absent
#   file(path)         trimmed file contents, "" when missing ("~/" expands)
#   coalesce(a, b, …)  first argument that is neither null nor ""
#   format(fmt, …)     printf-style formatting
#   pad_icons(text)    add a space after nerd-font (private-use) glyphs, which
#                      otherwise smush into the next character in the title
#                      bar's system font (used on space/tab in the default)
#
# Note: HCL has no "+" string concatenation — build strings with nested
# interpolation instead: "prefix ${getenv("VAR")} suffix".
template = "${coalesce(file("~/.local/var/herdr_window_title.${session}"), session)}${getenv("OVERSEER_CONTEXT_DISPLAY_NAME") != "" ? " : ${getenv("OVERSEER_CONTEXT_DISPLAY_NAME")}${getenv("OVERSEER_LOCATION_DISPLAY_NAME") != "" ? " @ ${getenv("OVERSEER_LOCATION_DISPLAY_NAME")}" : ""}" : ""} › ${pad_icons(space)} › ${pad_icons(tab)}${attention != "" ? " › ${attention}" : ""}"

env {
  # Command that prints the environment as NUL-separated KEY=VALUE pairs.
  # The default spawns your login shell interactively, so the plugin sees
  # exactly what a fresh terminal would — including Overseer's variables.
  # The command runs with a scrubbed minimal environment (HOME, USER, SHELL,
  # TERM, locale) so shell startup rebuilds everything from scratch; the herdr
  # server's own stale environment is never passed through.
  command = ["zsh", "-ilc", "env -0"]   # default: [$SHELL, "-ilc", "env -0"]

  # An interactive shell is slow (~0.8s) and herdr events arrive in bursts,
  # so the harvested environment is cached this long. The refresh action
  # (`herdr plugin action invoke davidolrik.window-title.refresh`) always bypasses
  # the cache.
  ttl = "10s"
}

attention {
  # Herdr agent states that count as "requires attention", in display order.
  # All states: idle, working, blocked, done, unknown.
  statuses = ["blocked", "done", "unknown"]

  # "all" counts agents in every space; "focused-space" only the current one.
  scope = "all"

  # Icon per status, rendered as "<icon><count>" (e.g. "×2"). A status
  # without an icon renders as "<status>:<count>". The defaults are herdr's
  # own "symbols" status indicators — the color-free variant of the sidebar
  # dots, since the title bar can't show color. Herdr's full set, should you
  # add more statuses: blocked "×", working "◐", done "✓", idle "○",
  # unknown "·".
  icons = {
    blocked = "×"
    done    = "✓"
    unknown = "·"
  }
}
