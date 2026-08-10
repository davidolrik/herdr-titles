# herdr-titles configuration.
#
# Location: $HERDR_PLUGIN_CONFIG_DIR/config.hcl
#   (run `herdr plugin config-dir davidolrik.titles` to find the directory;
#    without the plugin engine it falls back to ~/.config/herdr-titles/)
#
# Every value below shows the built-in default. Delete the file — or any part
# of it — to get the defaults back.

# The window title as an HCL template expression.
#
# Variables:
#   workspace  focused workspace label
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
#                      bar's system font (used on workspace/tab in the default)
#
# Note: HCL has no "+" string concatenation — build strings with nested
# interpolation instead: "prefix ${getenv("VAR")} suffix".
template = "${coalesce(file("~/.local/var/herdr_window_title.${session}"), session)}${getenv("OVERSEER_CONTEXT_DISPLAY_NAME") != "" ? " : ${getenv("OVERSEER_CONTEXT_DISPLAY_NAME")}${getenv("OVERSEER_LOCATION_DISPLAY_NAME") != "" ? " @ ${getenv("OVERSEER_LOCATION_DISPLAY_NAME")}" : ""}" : ""} › ${pad_icons(workspace)} › ${pad_icons(tab)}${attention != "" ? " › ${attention}" : ""}"

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
  # (`herdr plugin action invoke davidolrik.titles.refresh`) always bypasses
  # the cache.
  ttl = "10s"
}

# Automatic tab naming (ported from qu8n/herdr-automatic-rename, minus the
# jump-key numbering): each tab is named after its foreground program, or the
# shell name at a bare prompt. A manual rename opts that tab out permanently;
# to hand it back to automatic naming, rename it to just a space (herdr's
# rename UI rejects an empty name, but whitespace or a bare number both count
# as "cleared"), or run
# `herdr plugin action invoke davidolrik.titles.reset` from the tab.
# Real-time per-command renames come from the shell hooks under shell/ —
# source shell/hook.zsh (or .bash/.fish) from your shell startup.
tabs {
  # Master switch for tab naming; the window title works either way.
  enabled = true

  # 1 = a regular program shows its full command line ("psql -h db").
  show_program_args = false

  # Truncate program labels to this many characters (counted by codepoint).
  max_name_len = 20

  # Name shown at a bare prompt. Default: your $SHELL's basename.
  # shell_name = "zsh"

  # true = shell tabs get no name at all; herdr then shows its tab number.
  hide_shell = false

  # Programs that count as "a shell prompt", shown by their own name.
  shells = ["zsh", "bash", "sh", "fish", "dash", "ksh"]

  # Programs shown by name only, without command-line args (the herdr 0.8.0
  # agent detect set plus common tools; assigning replaces the default list).
  name_only_programs = [
    "nvim", "vim", "vi", "view", "gvim", "git", "lazygit", "gitui", "lazydocker",
    "claude", "codex", "aider", "pi", "gemini", "cursor", "cursor-agent", "devin",
    "agy", "antigravity", "cline", "omp", "mastracode", "opencode", "copilot",
    "kimi", "kiro", "kiro-cli", "droid", "amp", "grok", "hermes", "kilo", "qodercli",
  ]

  # Quick commands that keep showing the shell instead of taking over the tab.
  ignored_programs = [
    "ls", "eza", "ll", "la", "cd", "z", "zoxide", "cat", "bat", "less", "more",
    "echo", "pwd", "clear", "which", "man", "head", "tail", "wc", "cp", "mv",
    "rm", "mkdir", "touch", "fzf", "sudo", "doas",
  ]

  # Exact program renames; wins over every rule except the bare-prompt shell.
  # aliases = { lazygit = "lg" }

  # Ordered rewrites applied to the final label. Go RE2 regex syntax (NOT
  # sed -E as in herdr-automatic-rename; capture groups are $1, $2, ...).
  # substitute {
  #   pattern = ".*ipython([32])"
  #   replace = "ipython$1"
  # }

  # A pane hosting a recognized agent names its tab after the agent's session
  # title ("Fix flaky integration test") instead of the process name, icon
  # still in front. Titles get their own, longer truncation limit.
  agent_titles        = true
  agent_title_max_len = 40

  # Nerd Font glyph in front of program names (needs a Nerd Font).
  icons {
    enabled  = false
    style    = "name_and_icon" # name_and_icon | name | icon
    fallback = "?"             # glyph for unmapped programs; "" = none
    # map    = { nvim = "" } # per-program overrides, win over the builtin table
  }
}

attention {
  # Herdr agent states that count as "requires attention", in display order.
  # All states: idle, working, blocked, done, unknown.
  statuses = ["blocked", "done", "unknown"]

  # "all" counts agents in every workspace; "focused-workspace" only the current one.
  scope = "all"

  # Icon per status, rendered as "<icon><count>" (e.g. "×2"). A status
  # without an icon renders as "<status>:<count>". The defaults are herdr's
  # own "symbols" status indicators — the color-free variant of the sidebar
  # dots, since the title bar can't show color. Every state has an icon here;
  # only the statuses listed above appear in the title, so enabling e.g.
  # working is a statuses-only edit.
  icons = {
    idle    = "○"
    working = "◐"
    blocked = "×"
    done    = "✓"
    unknown = "·"
  }
}
