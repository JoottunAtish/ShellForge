# Shellforge shell instrumentation.
#
# Sourced via:  bash --rcfile /opt/shellforge/rc/instrument.bash -i
#
# This file is how the game knows what the learner typed, what it exited with,
# where they were, and how long it took, WITHOUT intercepting or re-executing a
# single command. The shell stays a real bash: vim, less, htop, job control, tab
# completion and Ctrl-C all behave exactly as they would anywhere else.
#
# The mechanism is OSC 133 semantic prompt markers. The host side of the PTY
# parses them out of the byte stream and strips them before rendering, so the
# learner never sees them.
#
#   133;A          prompt start
#   133;B          command input start
#   133;C          pre-execution
#   133;D;<exit>   command finished, with its exit status
#   7;file://...   report the working directory
#
# Read-only, bind-mounted at /opt/shellforge/rc/. Do not edit inside the sandbox.

# ---------------------------------------------------------------------------
# Inherit the real system and user configuration first. The learner's own
# .bashrc must still work; this file wraps it, it does not replace it.
# ---------------------------------------------------------------------------
[ -f /etc/bash.bashrc ] && . /etc/bash.bashrc
[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"

# ---------------------------------------------------------------------------
# State locations. SF_STATE is injected by the runtime and is per level.
# ---------------------------------------------------------------------------
: "${SF_STATE:=/opt/shellforge/state}"
SF_JOURNAL="${SF_STATE}/journal.tsv"
SF_ENV="${SF_STATE}/env.snapshot"
SF_ALIASES="${SF_STATE}/alias.snapshot"
SF_FUNCS="${SF_STATE}/func.snapshot"
SF_SHOPTS="${SF_STATE}/shopt.snapshot"

mkdir -p "${SF_STATE}" 2>/dev/null || true

# ---------------------------------------------------------------------------
# Prompt markers.
#
# The \[ and \] readline markers around the escape sequences are REQUIRED. They
# tell readline the enclosed bytes occupy zero display columns. Without them,
# bash miscounts the prompt width and line wrapping corrupts as soon as the
# learner types past the terminal edge. This is the single most common way to
# get OSC 133 subtly wrong.
# ---------------------------------------------------------------------------
PS1='\[\e]133;A\a\]'"${PS1:-\u@quest:\w\$ }"'\[\e]133;B\a\]'

# PS0 is emitted after the command is read but before it runs. No readline
# markers here: PS0 is not part of the prompt readline is measuring.
PS0='\e]133;C\a'

# ---------------------------------------------------------------------------
# Percent-encoding for the OSC 7 cwd report.
#
# The OSC 7 payload written below is a file:// URI, and a URI is
# percent-encoded by definition. internal/pty's parser decodes that payload
# with Go's url.PathUnescape and finds the path by splitting on the first '/'
# after the host, so the path's internal '/' separators must never be
# percent-encoded, only the bytes within each path segment. This function
# percent-encodes every byte of its argument that is not RFC 3986 unreserved
# (letters, digits, '-', '.', '_', '~'), except '/', which passes through
# unencoded because it is the path separator, not a byte to encode.
#
# It writes its result to the global __sf_encoded rather than printing and
# being captured with $(...), because command substitution forks a subshell
# and this file's whole point is that nothing in it forks: no subprocess,
# only bash builtins (printf, parameter expansion, a C-style for loop).
#
# LC_ALL=C makes bash index the string one byte at a time, not one multibyte
# character at a time. That is what makes a path containing a multi-byte
# UTF-8 character come out correct: this function has no notion of what a
# UTF-8 code point is, it just encodes raw bytes one at a time, and
# url.PathUnescape on the decoding side reassembles those bytes into the same
# UTF-8 character it started as.
# ---------------------------------------------------------------------------
# BEGIN __sf_urlencode
__sf_urlencode() {
  local LC_ALL=C s="$1" i c hex
  __sf_encoded=
  for (( i = 0; i < ${#s}; i++ )); do
    c="${s:i:1}"
    case "$c" in
      [a-zA-Z0-9._~/-]) __sf_encoded+="$c" ;;
      *)
        printf -v hex '%02X' "'$c"
        __sf_encoded+="%$hex"
        ;;
    esac
  done
}
# END __sf_urlencode

# ---------------------------------------------------------------------------
# Post-command hook.
#
# Capturing $? on the very first line is not stylistic. Any command before it,
# including a local declaration with an assignment, overwrites the exit status
# the game is trying to record.
# ---------------------------------------------------------------------------
__sf_after() {
  local ec=$?

  # Semantic markers for the host parser.
  printf '\e]133;D;%s\a' "$ec"
  __sf_urlencode "$PWD"
  printf '\e]7;file://quest%s\a' "$__sf_encoded"

  # Durable journal, readable from inside the sandbox by check scripts and
  # surviving a crash of the host process.
  local line
  line=$(HISTTIMEFORMAT='' history 1)
  line=${line#"${line%%[![:space:]]*}"}   # strip leading whitespace
  line=${line#* }                         # strip the history number
  line=${line#"${line%%[![:space:]]*}"}   # strip the space after it
  printf '%s\t%s\t%s\t%s\n' "${EPOCHREALTIME}" "$ec" "$PWD" "$line" >> "$SF_JOURNAL" 2>/dev/null

  # Shell-local state snapshots.
  #
  # Verification checks run in a SEPARATE non-interactive shell, so they cannot
  # see `export FOO=bar` typed here. These snapshots are the only way an
  # env_var or cwd_is check can work at all. Level env-01 is unsolvable without
  # them.
  env -0    > "$SF_ENV"     2>/dev/null
  alias -p  > "$SF_ALIASES" 2>/dev/null
  declare -F > "$SF_FUNCS"  2>/dev/null
  shopt -p  > "$SF_SHOPTS"  2>/dev/null

  return $ec
}
PROMPT_COMMAND='__sf_after'

# ---------------------------------------------------------------------------
# History. The journal is the source of truth, but `history 1` above reads from
# this, so it has to be complete.
#
# HISTCONTROL is explicitly unset: the defaults drop duplicates and
# space-prefixed commands, and the game needs every command including the
# repeated ones. "You retyped that path four times, try Tab" depends on it.
# ---------------------------------------------------------------------------
export HISTFILE="${SF_STATE}/bash_history"
export HISTSIZE=100000
export HISTFILESIZE=100000
unset HISTCONTROL
unset HISTIGNORE
shopt -s histappend

# ---------------------------------------------------------------------------
# In-sandbox shims: check, hint, brief, reset.
#
# Prepending rather than appending means the learner types `check` and gets the
# game, not something else on the PATH.
# ---------------------------------------------------------------------------
export PATH="/opt/shellforge/bin:$PATH"

# ---------------------------------------------------------------------------
# Mild anti-tamper.
#
# This stops an accidental `PROMPT_COMMAND=` from silently disabling scoring. It
# is deliberately not a security control: the learner owns this shell and could
# defeat it in a dozen ways. The threat here is a mistake, not an adversary.
# ---------------------------------------------------------------------------
readonly PROMPT_COMMAND
readonly PS0
readonly SF_JOURNAL
readonly SF_ENV
