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

# Resolve the engine next to this file, so the hook works no matter where the
# plugin is installed. ${(%):-%N} is the zsh-standard way to read the sourced
# file's own path and is immune to FUNCTION_ARGZERO being toggled, unlike $0.
_hwt_self="${(%):-%N}"
_hwt_bin="${_hwt_self:A:h:h}/bin/herdr-titles"
unset _hwt_self

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
  _hwt_preexec() {
    local line="${2:-$1}" kind
    kind=$(builtin whence -w -- "${(Q)${(z)line}[1]}" 2>/dev/null)
    case "${kind##*: }" in
      command|hashed) ("$_hwt_bin" preexec "$line"       >/dev/null 2>&1 &) ;;
      *)              ("$_hwt_bin" preexec "$line" shell >/dev/null 2>&1 &) ;;
    esac
  }
  _hwt_precmd() { ("$_hwt_bin" precmd zsh >/dev/null 2>&1 &); }

  autoload -Uz add-zsh-hook
  add-zsh-hook preexec _hwt_preexec   # add-zsh-hook is idempotent on re-source
  add-zsh-hook precmd  _hwt_precmd
fi
