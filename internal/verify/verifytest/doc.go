// Package verifytest holds a fake runtime.Session for testing
// internal/verify checks against scripted sandbox state, with no Docker
// daemon and no real bash.
//
// Layer L3, alongside internal/verify itself. It may import internal/runtime
// and below, and only a _test.go file may import it: linking a fake session
// into the product binary would be a bug. The Go toolchain cannot enforce
// that on its own, since this package has no _test.go suffix of its own and
// does not import testing, so the rule is enforced by a source-parsing test,
// TestVerifytestIsImportedOnlyByTests in internal/verify.
//
// Every check in internal/verify only ever calls Session.Exec: Attach,
// PushFiles, and PullFile exist to serve the learner's interactive shell and
// the level setup pipeline, neither of which a check touches. Session's four
// methods other than Exec panic here, on the theory that a check calling one
// of them is a bug in the check, not a scenario a test should quietly
// tolerate.
package verifytest
