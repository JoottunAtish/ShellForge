// Package content loads and validates level packs.
//
// Layer L3. Peer of internal/verify: neither package may import the other.
// May import internal/runtime, internal/journal, and below.
//
// The peer rule is load bearing and no gate enforces it: internal/archtest's
// collectImports skips every _test.go file, so a test-only import crossing
// this boundary is invisible to it. This package pays a real cost to hold
// the line, in packcontent_test.go's hand-maintained mirror of
// internal/verify's parseScope, and fixture_test.go's own TypeChecker fake.
// An earlier revision of this comment claimed the import was allowed, which
// is how #154's first pass came to add one. See issue #157.
//
// Levels are declarative YAML, specified in docs/LEVEL-FORMAT.md. The validator
// enforces the authoring invariants listed there, and its error messages must
// name the file, the level id, and the field.
//
// Pack-supplied paths are untrusted input. A `source:` must resolve inside the
// pack directory, and a `path:` must resolve under setup.root, which must itself
// be under /home/learner/. Reject traversal, do not sanitize it.
//
// Strip the \r of a \r\n pair from every file materialized into the sandbox,
// per the reason recorded on stripCRLF in internal/content/setup.
package content
