// Package doctor implements the preflight diagnostics behind `shellforge doctor`.
//
// Layer L0. May import internal/platform. Must not import a runtime
// implementation; probe for Docker and WSL through the platform layer.
//
// Every probe returns {Status, Detail, Remediation, DocAnchor}. The DocAnchor
// must name a heading that exists in docs/05-troubleshooting.md, and CI fails
// the build when one does not. This package is the single highest return on
// investment in the project: it is the difference between a learner fixing
// their own machine and a learner giving up at step three.
package doctor
