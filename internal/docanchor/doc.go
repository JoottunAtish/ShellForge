// Package docanchor enforces the living contract between the code and
// docs/05-troubleshooting.md: every doc anchor a user-facing error can emit
// must resolve to a heading a learner can actually land on.
//
// It replaces the CI grep that used to do this job. That grep matched only
// the struct-literal shape, DocAnchor: "...", and every real call site in
// this repository uses the positional form, ux.Fail(op, err, remediation,
// "..."), so the grep had been finding one match, a comment, and reporting
// green while checking nothing. See issue #86.
//
// The test in this package parses every non-test .go file in the module,
// finds both shapes, resolves a string literal or a package-level string
// constant used as the anchor argument, and reports anything else as
// unverifiable rather than silently passing it. It sits outside the layer
// rule, alongside internal/archtest, because it is a governance test over
// the whole module rather than a runtime layer.
//
// It also follows a forwarder: an unexported helper that takes its caller's
// anchor and hands it to ux.Fail is not an anchor site itself, so its call
// sites are checked in its place. Forwarders are discovered from the source
// rather than registered in a list, because a rule enforced by a list
// somebody has to remember to extend is a rule that decays. See issue #132.
//
// This is the only implementation of the rule. Three package-local copies
// preceded it, in cmd/shellforge, internal/sandbox and internal/store, and
// all of them are retired: each was an AST walk of the same shape, scoped
// to one package, and two of them drifting apart was a question of when.
// internal/doctor keeps its own anchor tests, which is not a fourth copy:
// they drive every probe's Run and check the anchor carried at runtime,
// which no walk over source can see.
//
// This file exists so the package has a non-test Go source file and
// therefore builds under `go build ./...`.
package docanchor
