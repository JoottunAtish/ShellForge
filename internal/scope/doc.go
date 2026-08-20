// Package scope holds the vocabulary type that says which slice of command
// history a journal check reads.
//
// Layer L0. Standard library only, and in fact it imports nothing at all.
//
// It exists because two packages that may never import each other need to
// name the same type. internal/journal is layer 2 and internal/verify is
// layer 3, so journal cannot import verify (that is the upward edge
// internal/archtest forbids) and verify does not import journal by its own
// deliberate contract (see internal/verify/doc.go). A type both of them need
// therefore has to live below both, which is here.
//
// internal/verify declares Scope and ScopeKind as type aliases to this
// package's, so verify.Scope and scope.Scope are one type rather than two
// that happen to match. That is what lets *journal.Journal satisfy
// verify.JournalReader with no adapter and no upward import.
//
// Add a new ScopeKind here only together with the arm that answers it in
// internal/journal's commands, and the parser that produces it in
// internal/verify's parseScope. A kind that reaches journal with no matching
// arm falls through to journal's default and becomes an error nobody asked
// for.
//
// The three string values are the level YAML surface: a level writes
// scope: level, scope: last, or scope: last_n:N. Renaming one silently
// breaks every level that uses it, so a test pins the strings.
package scope
