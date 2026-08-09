# herdr-titles: live per-command tab auto-naming (bash).
# Adapted from qu8n/herdr-automatic-rename's shell/hook.bash (MIT, (c) Quan Nguyen).
#
# bash has no native preexec, and the DEBUG trap + PROMPT_COMMAND are SHARED,
# exclusive resources that tools like atuin, bash-preexec, ble.sh and starship
# rely on. Overwriting either silently breaks them, so we cooperate:
#   * If preexec_functions / precmd_functions already exist (the bash-preexec /
#     atuin convention), just register into them -- that framework owns the trap
#     and calls us with the command line as $1. Nothing to clobber.
#   * Otherwise drive precmd from PROMPT_COMMAND and preexec from a DEBUG trap,
#     but install the DEBUG trap ONLY if no other tool has one (checked via
#     `trap -p DEBUG`). If one exists we leave it alone and skip live preexec;
#     precmd still runs.
#
# Source this AFTER your prompt / history tools so their arrays already exist:
#   source /path/to/herdr-titles/shell/hook.bash
#
# No-ops outside a herdr pane and when the engine is not found. Needs bash 3.1+.

_hwt_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." 2>/dev/null && pwd)
_hwt_bin="$_hwt_root/bin/herdr-titles"

# The _hwt_installed latch makes re-sourcing (e.g. `source ~/.bashrc`) a no-op,
# so PROMPT_COMMAND never grows a second entry and we never double-register.
if [[ -n ${HERDR_PANE_ID:-} && -x $_hwt_bin && -z ${_hwt_installed:-} ]]; then
  _hwt_installed=1

  # Background in a subshell so bash never prints a "[1] <pid>" job-start line.
  # The first word only names a program when `type -t` says it is a file on
  # disk; anything else gets a "shell" marker telling the engine to sample the
  # pane's real foreground process instead.
  _hwt_preexec() {
    local word="${1%% *}" kind
    kind=$(type -t -- "$word" 2>/dev/null)
    if [ "$kind" = "file" ]; then
      ("$_hwt_bin" preexec "$1"       >/dev/null 2>&1 &)
    else
      ("$_hwt_bin" preexec "$1" shell >/dev/null 2>&1 &)
    fi
  }
  _hwt_precmd() { ("$_hwt_bin" precmd bash >/dev/null 2>&1 &); }

  if declare -p preexec_functions >/dev/null 2>&1 || declare -p precmd_functions >/dev/null 2>&1; then
    # A preexec framework (bash-preexec / ble.sh / atuin) owns the trap; just
    # register and let it dispatch us. Detect the arrays with `declare -p`, not
    # ${arr+x}: bash-preexec declares them EMPTY, and ${arr+x} tests element 0
    # (unset for an empty array) so it would miss them.
    case " ${preexec_functions[*]} " in *" _hwt_preexec "*) : ;; *) preexec_functions+=(_hwt_preexec) ;; esac
    case " ${precmd_functions[*]} "  in *" _hwt_precmd "*)  : ;; *) precmd_functions+=(_hwt_precmd)   ;; esac
  else
    # Standalone: drive precmd from PROMPT_COMMAND and preexec from a DEBUG
    # trap -- but NEVER clobber a DEBUG trap another tool already installed.
    #
    # Latch: fire preexec only for the FIRST command after a prompt. Start
    # DISARMED (=1) so the trap fires neither on this hook's own trailing lines
    # nor on a pre-existing PROMPT_COMMAND entry before our wrap first runs;
    # each precmd re-arms it (=0), firing disarms it.
    _hwt_fired=1
    _hwt_debug() {
      [[ -n ${COMP_LINE:-} ]] && return            # programmable completion
      [[ ${BASH_SUBSHELL:-0} -gt 0 ]] && return    # command substitutions etc.
      [[ $_hwt_fired == 1 ]] && return             # already fired this prompt
      case "$BASH_COMMAND" in _hwt_*) return ;; esac
      _hwt_fired=1
      _hwt_preexec "$BASH_COMMAND"
    }

    # Append our wrap LAST in PROMPT_COMMAND so re-arming happens after every
    # other precmd entry; preserve $? for anything downstream that reads it.
    _hwt_precmd_wrap() {
      local _hwt_st=$?
      _hwt_fired=0
      _hwt_precmd
      return $_hwt_st
    }
    PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND$'\n'}_hwt_precmd_wrap"

    # Own the DEBUG trap only when nothing else holds it. Installed LAST:
    # nothing in this hook runs at top level afterward, so the trap has no
    # trailing setup line of ours to fire on.
    if [[ -z $(trap -p DEBUG 2>/dev/null) ]]; then
      trap '_hwt_debug' DEBUG
    fi
  fi
fi
