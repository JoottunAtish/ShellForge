// Package pty is the terminal instrumentation layer: a PTY multiplexer and a
// streaming OSC escape sequence parser.
//
// Layer L2. May import internal/runtime and below.
//
// This is the highest-risk code in the project. Two rules govern it.
//
// First, the parser is a streaming byte-level state machine, not a regular
// expression over a buffer. Escape sequences arrive split across Read calls at
// arbitrary offsets, and a buffer-based implementation drops markers roughly one
// percent of the time.
//
// Second, only recognized sequences are stripped from the forwarded stream.
// Every other byte is forwarded unmodified. vim depends on that being literally
// true.
//
// A second risk category, alongside the parser's streaming state machine: the
// multiplexer in mux.go holds the host terminal in raw mode for the whole
// session, and every exit path, a panic included, must restore it before
// returning. A terminal left in raw mode looks broken to the learner: keystrokes
// stop echoing back and there is no way out short of closing the window. Every
// exit path in mux.go goes through one sync.Once, so a deferred restore, a
// SIGINT and SIGTERM safety net, and a panic recovery path can never restore
// twice, and none of them can skip it either.
//
// Required tests: markers split at every byte offset, a recorded vim session
// that passes through byte-identical apart from our markers, user-typed fake
// OSC 133 sequences that must not corrupt parser state, a fuzz target, and,
// for the multiplexer, terminal restoration proven across every exit path
// including a panic.
package pty
