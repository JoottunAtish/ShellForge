// Package setup runs the transactional setup and teardown of a level's world
// inside the sandbox.
//
// Layer L3. It imports internal/content (same layer), internal/runtime (L1),
// and internal/platform plus internal/platform/ux (L0). It must never import
// internal/game, cmd/shellforge, or any runtime backend package. The arrow
// between this package and internal/content points one way only: this
// package imports internal/content, and internal/content must never import
// this package. Both packages sit at layer 3, so internal/archtest's layer
// numbers cannot catch a cycle between them; only the Go compiler's import
// cycle detection would, and only once the cycle is complete. This comment
// is the rule stated where a reader of either package will find it.
//
// Setup is teardown-first: every Setup call removes whatever is already
// there before staging anything new, which is what makes it safe to call
// twice in a row. A failure partway through rolls back by running Teardown
// again on a context that survives the caller's own cancellation, so a
// Ctrl-C at the moment of failure still leaves the sandbox clean. See
// runner.go's doc comments for the full contract.
//
// Every deletion in this package runs inside the sandbox through
// runtime.Session.Exec. There is no host-side filesystem removal anywhere in
// this package; TestNoHostFilesystemRemovalInPackage in runner_test.go pins
// that by parsing the package's own source.
//
// A Runner is not safe for concurrent use: one level is set up at a time.
// The generator registry in generate.go is package level and guarded by a
// mutex, so RegisterGenerator is safe to call from another package's init.
package setup
