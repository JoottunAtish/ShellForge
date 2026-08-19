// Package doctor implements the preflight diagnostics behind `shellforge doctor`.
//
// Layer L0. It may import, from this module, exactly internal/platform and
// internal/platform/ux, and nothing else. It must never import
// internal/runtime or a runtime backend: sandbox_health goes through the
// SandboxProber interface declared in this package and supplied by the
// caller at L5, so the only edge is L5 importing L0, which points downward.
//
// No probe mutates anything. Fix is the only path in this package that
// changes the machine, it runs only on request, it never elevates, and it
// never removes anything: its whole allowlist is one call to
// platform.EnsureDir(platform.DataDir()).
//
// Every probe returns {Status, Detail, Remediation, DocAnchor}. The DocAnchor
// must name a heading that exists in docs/05-troubleshooting.md, and
// anchors_test.go fails the build when one does not. This package is the
// single highest return on investment in the project: it is the difference
// between a learner fixing their own machine and a learner giving up at step
// three.
package doctor
