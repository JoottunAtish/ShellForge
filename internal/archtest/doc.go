// Package archtest enforces the layer dependency rule from CLAUDE.md.
//
// The rule: dependencies point downward only. internal/game must never import a
// Docker or WSL type. This is what makes "drop WSL support" a one-day decision
// instead of a rewrite, which is a real scheduled decision point on Day 3 of the
// build plan.
//
// The test in this package parses the imports of every non-test Go file in the
// module and fails on an upward edge, on a reach into a runtime implementation
// from above L1, or on a network client appearing at or above L3.
//
// If you need to add a legitimate edge, edit the tables in layers_test.go and
// justify it in the commit message. Do not delete the test.
//
// This file exists so the package has a non-test Go source file and therefore
// builds under `go build ./...`.
package archtest
