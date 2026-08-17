# herdr-titles: live per-command tab auto-naming (fish).
# Adapted from qu8n/herdr-automatic-rename's shell/hook.fish (MIT, (c) Quan Nguyen).
#
# fish_preexec fires right before a command runs (with the command line in
# $argv[1]); fish_postexec fires when back at the prompt.
#
# Source this from your fish config (~/.config/fish/config.fish):
#   source /path/to/herdr-titles/shell/hook.fish
#
# No-ops outside a herdr pane ($HERDR_PANE_ID) and when the engine is not
# found. Runs are backgrounded so the prompt never blocks on herdr.

# Resolve the engine and stash it in a GLOBAL: fish function bodies do not
# close over sourcing-time locals, but they do see globals at call time. When
# sourced from the plugin dir this reads the sourced file's own path;
# `herdr-titles init fish` emits this same hook with the engine's absolute
# path baked in on this line instead.
set -g _hwt_bin (dirname (dirname (status current-filename)))/bin/herdr-titles # HWT_BIN

if test -n "$HERDR_PANE_ID"; and test -x "$_hwt_bin"
    # The first word only names a program when `type --type` says "file" (a
    # real executable). fish aliases ARE functions and fish wraps some
    # externals in functions (`ls`, `cd`), so anything that is not a file gets
    # a "shell" marker telling the engine to sample the pane's real foreground
    # process instead.
    function _hwt_preexec --on-event fish_preexec
        set -l word (string split -m 1 ' ' -- $argv[1])[1]
        set -l kind (type --type -- $word 2>/dev/null)
        if test "$kind" = file
            command "$_hwt_bin" preexec "$argv[1]" >/dev/null 2>&1 &
        else
            command "$_hwt_bin" preexec "$argv[1]" shell >/dev/null 2>&1 &
        end
        disown 2>/dev/null
    end

    function _hwt_precmd --on-event fish_postexec
        command "$_hwt_bin" precmd fish >/dev/null 2>&1 &
        disown 2>/dev/null
    end
end

# Pane title for `tabs { terminal_titles = true }`: fish already sets a
# terminal title every prompt through its fish_title function, which herdr
# records as the pane's title — so nothing to add here. To choose the text,
# define your own fish_title (see `help fish_title`).
