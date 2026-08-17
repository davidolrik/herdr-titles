# herdr-titles: live per-command tab auto-naming (zsh).
# Adapted from qu8n/herdr-automatic-rename's shell/hook.zsh (MIT, (c) Quan Nguyen).
#
# herdr has no "foreground command changed" event, so these zsh hooks give the
# real-time updates: preexec renames the tab after the command that is starting,
# precmd renames it after the shell name once back at the prompt. The plugin's
# herdr [[events]] handle everything else. A lock in the engine keeps the hook
# and events from racing, and the engine honors the tabs.enabled toggle (this
# hook stays dumb).
#
# Source this from your zsh startup (point at wherever the plugin lives):
#   source /path/to/herdr-titles/shell/hook.zsh
#
# No-ops outside a herdr pane (herdr injects $HERDR_PANE_ID/$HERDR_TAB_ID) and
# when the engine is not found next to this file. Runs are backgrounded so the
# prompt never blocks on herdr.

# Resolve the engine. When sourced from the plugin dir this reads the sourced
# file's own path (the zsh prompt-expansion form of it, immune to
# FUNCTION_ARGZERO being toggled, unlike $0); `herdr-titles init zsh` emits
# this same hook with the engine's absolute path baked in on this line instead.
_hwt_bin="${${(%):-%N}:A:h:h}/bin/herdr-titles" # HWT_BIN

if [[ -n ${HERDR_PANE_ID:-} && -x $_hwt_bin ]]; then
  # Background in a subshell so the interactive shell never prints the
  # "[1] <pid>" job-start line (& disown only hides the completion notice).
  #
  # zsh hands preexec three forms of the command: $1 is what the user typed
  # (aliases NOT expanded), $2 is a single-line alias-expanded version, and $3
  # is the full expanded text. Pass $2 so an alias like `..` (-> `cd ..`) names
  # the tab by its real program; fall back to $1 when history is off.
  #
  # The first word is only a program name when it resolves to an external
  # command; whence -w classifies it. command/hashed keep the instant path,
  # anything else (function, builtin, reserved word, typo) gets a "shell"
  # marker telling the engine to sample the pane's real foreground process.
  #
  # Under `tabs { terminal_titles = true }` the daemon is the single writer
  # for the tab, and the program a command starts reaches it through the pane
  # TITLE: for a real program, publish its basename as the title here (OSC 2,
  # to $HERDR_TITLES_TTY — a test seam — or /dev/tty). Builtins and functions
  # (cd, z, aliases) publish nothing, so the prompt's cwd title stands and the
  # tab never flashes the shell name; a program that sets its own title
  # (nvim) simply overrides this a moment later. Harmless when terminal
  # titles are off. HERDR_TITLES_NO_TITLE=1 disables it like the prompt title.
  _hwt_preexec() {
    local line="${2:-$1}" kind word
    # Split into an explicit array: a single-word line makes ${(z)line}
    # collapse to a scalar, and [1] would then take its first CHARACTER.
    local -a words; words=("${(z)line}")
    word="${(Q)words[1]}"
    kind=$(builtin whence -w -- "$word" 2>/dev/null)
    case "${kind##*: }" in
      command|hashed)
        [[ -z ${HERDR_TITLES_NO_TITLE:-} ]] && print -n "\e]2;${word:t}\a" > "${HERDR_TITLES_TTY:-/dev/tty}" 2>/dev/null
        ("$_hwt_bin" preexec "$line"       >/dev/null 2>&1 &) ;;
      *)              ("$_hwt_bin" preexec "$line" shell >/dev/null 2>&1 &) ;;
    esac
  }
  _hwt_precmd() { ("$_hwt_bin" precmd zsh >/dev/null 2>&1 &); }

  autoload -Uz add-zsh-hook
  add-zsh-hook preexec _hwt_preexec   # add-zsh-hook is idempotent on re-source
  add-zsh-hook precmd  _hwt_precmd

  # Pane title for `tabs { terminal_titles = true }`: zsh sets no terminal
  # title on its own, so publish one every prompt (OSC 2, which herdr records
  # as the pane's title — it never reaches the host window title). Define
  # _herdr_titles_title yourself BEFORE this hook to choose the text (it
  # defaults to the cwd basename), or set HERDR_TITLES_NO_TITLE=1 to keep
  # your shell's own title handling. Harmless when terminal_titles is off.
  if [[ -z ${HERDR_TITLES_NO_TITLE:-} ]]; then
    # HWT_SHELL_ICON is the shell's icon glyph (plus a space) when `init`
    # emitted this with icons enabled, "" otherwise — so a plain-shell tab
    # named after its cwd looks like every other tab. A user-defined
    # _herdr_titles_title replaces the whole string, glyph included.
    _hwt_shell_icon="" # HWT_SHELL_ICON
    (( ${+functions[_herdr_titles_title]} )) || _herdr_titles_title() { print -r -- "${_hwt_shell_icon}${PWD:t}"; }
    _herdr_titles_precmd() { print -n "\e]2;$(_herdr_titles_title)\a" > "${HERDR_TITLES_TTY:-/dev/tty}" 2>/dev/null; }
    add-zsh-hook precmd _herdr_titles_precmd
  fi
fi
