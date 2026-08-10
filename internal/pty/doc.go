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
// Required tests: markers split at every byte offset, a recorded vim session
// that passes through byte-identical apart from our markers, user-typed fake
// OSC 133 sequences that must not corrupt parser state, and a fuzz target.
package pty
