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
// This file exists so the package has a non-test Go source file and
// therefore builds under `go build ./...`.
package docanchor
