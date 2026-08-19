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

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/platform"
)

// probeTimeout bounds a single probe. Five seconds is long enough for a
// healthy `docker version` on a cold Docker Desktop and short enough that
// twelve of them cannot outlast a learner's patience.
const probeTimeout = 5 * time.Second

// The twelve anchor constants. Every one of these already exists as a
// heading in docs/05-troubleshooting.md; anchors_test.go is the gate that
// keeps that true.
const (
	docAnchorOSTooOld          = "os-too-old"
	docAnchorVirtDisabled      = "virtualization-disabled"
	docAnchorVMPlatformMissing = "vm-platform-missing"
	docAnchorWSLNotInstalled   = "wsl-not-installed"
	docAnchorWSLVersion1       = "wsl-version-1"
	docAnchorWSLKernelOutdated = "wsl-kernel-outdated"
	docAnchorDockerNotFound    = "docker-not-found"
	docAnchorDockerDaemonDown  = "docker-daemon-down"
	docAnchorDiskSpaceLow      = "disk-space-low"
	docAnchorTerminalNoVT      = "terminal-no-vt"
	docAnchorTerminalNotWT     = "terminal-not-windows-terminal"
	docAnchorSandboxUnhealthy  = "sandbox-unhealthy"
)

// Level is how bad a finding is.
type Level int

const (
	// OK means the machine satisfies this check.
	OK Level = iota
	// Warn means something is worth knowing but nothing is blocked.
	Warn
	// Fail means Shellforge will not work until this is fixed.
	Fail
)

// String returns the lowercase word this level renders as, in the table and
// in JSON. The zero Level is OK, so a zero Result reads as passing; every
// Result this package builds sets Status explicitly.
func (l Level) String() string {
	switch l {
	case Warn:
		return "warn"
	case Fail:
		return "fail"
	default:
		return "ok"
	}
}

// MarshalJSON encodes a Level as its lowercase word, not as a number, so
// that a shell script reading `shellforge doctor --json` can compare
// against "fail" without depending on the order of the constants.
func (l Level) MarshalJSON() ([]byte, error) {
	return []byte(`"` + l.String() + `"`), nil
}

// TODO(v0.2): Level has no UnmarshalJSON. Nothing in v0.1 reads a doctor
// report back in, and a decoder nobody calls is a decoder nobody tests.

// Probe is one machine check. A probe never mutates anything.
type Probe interface {
	ID() string
	// AppliesTo reports whether this probe is meaningful on this platform.
	// A probe that does not apply is omitted from the report, never
	// reported as a failure.
	AppliesTo(goos string) bool
	Run(ctx context.Context) Result
}

// Result is what one probe observed. Every field except Fixable is
// non-empty on every Result this package builds, including a passing one,
// so that a caller scripting against the JSON never has to special-case a
// missing field.
type Result struct {
	ID          string
	Status      Level
	Detail      string // what was observed
	Remediation string // the next command to run, or the next thing to do
	DocAnchor   string // a heading id in docs/05-troubleshooting.md, no '#'
	Fixable     bool   // --fix can perform this reversibly, without elevation
}

// Report is every applicable probe's result, in probe order.
type Report struct {
	Results  []Result
	Platform string // runtime.GOOS, as the report was gathered
	Version  string // the shellforge build version, set by the caller
}

// Failed reports whether any result is Fail. A Warn is not a failure and
// does not change the exit status.
func (r Report) Failed() bool {
	for _, res := range r.Results {
		if res.Status == Fail {
			return true
		}
	}
	return false
}

// SandboxProber is the narrow view of a sandbox the health probe needs. The
// caller at L5 supplies it, so internal/doctor never imports internal/runtime.
// A nil SandboxProber is valid and makes sandbox_health report warn with a
// detail saying no runtime was supplied.
type SandboxProber interface {
	Ping(ctx context.Context) (provisioned, running bool, detail string, err error)
}

// Run executes every applicable probe in order and collects the results. It
// honours ctx: a hung subprocess must not hang doctor. Each probe is
// bounded individually, so one slow probe costs its own budget and not the
// report's.
//
// Report.Version is left empty. The build version lives in package main
// and an L0 package cannot reach it, so the caller sets it on the returned
// Report.
func Run(ctx context.Context, p SandboxProber) Report {
	return run(ctx, p, newExecRunner(), probeTimeout)
}

// run is the seam every test drives: it takes the subprocess runner and the
// per-probe budget so the suite is hermetic and fast.
func run(ctx context.Context, p SandboxProber, r runner, budget time.Duration) Report {
	probes := applicableProbes(runtime.GOOS, p, r)
	results := make([]Result, 0, len(probes))
	for _, probe := range probes {
		pctx, cancel := context.WithTimeout(ctx, budget)
		results = append(results, probe.Run(pctx))
		cancel()
	}
	return Report{Results: results, Platform: runtime.GOOS}
}

// Fix performs only the reversible, non-privileged remediations. It returns
// what it did, then what it deliberately refused to do and printed instead.
//
// Fix never elevates, never writes .wslconfig, never touches a WSL
// distribution, and never removes anything.
func Fix(ctx context.Context, r Report) (done, refused []string, err error) {
	return fix(ctx, r, fixActions())
}

// fix is the testable form. actions is a compile-time allowlist keyed by
// probe id: a Result claiming Fixable for an id that is not in the
// allowlist is refused, never attempted, so a probe cannot talk Fix into an
// action Fix does not own.
func fix(ctx context.Context, r Report, actions map[string]fixAction) (done, refused []string, err error) {
	for _, res := range r.Results {
		if res.Status == OK {
			continue
		}
		action, owned := actions[res.ID]
		if !res.Fixable || !owned {
			refused = append(refused, fmt.Sprintf("%s: %s", res.ID, res.Remediation))
			continue
		}
		if applyErr := action.apply(ctx); applyErr != nil {
			return done, refused, fmt.Errorf("fix %s: %w", res.ID, applyErr)
		}
		done = append(done, fmt.Sprintf("%s: %s", res.ID, action.describe))
	}
	return done, refused, nil
}

// anchored is the optional extra every probe in this package satisfies, so
// that a probe can name its own troubleshooting anchor on a path where it
// produced no observation of its own.
type anchored interface{ anchor() string }

// baseProbe carries the three facts every probe shares and builds every
// Result, so no probe hand-rolls a struct literal and forgets a field.
type baseProbe struct {
	id          string
	docAnchor   string
	windowsOnly bool
}

func (b baseProbe) ID() string                 { return b.id }
func (b baseProbe) AppliesTo(goos string) bool { return !b.windowsOnly || goos == "windows" }
func (b baseProbe) anchor() string             { return b.docAnchor }

// result builds a Result for this probe. remediation is required, including
// on an OK result, because the JSON contract says every field is
// non-empty.
func (b baseProbe) result(status Level, detail, remediation string) Result {
	return Result{ID: b.id, Status: status, Detail: detail, Remediation: remediation, DocAnchor: b.docAnchor}
}

// fixableResult is result plus Fixable true. Only diskFreeProbe calls it.
func (b baseProbe) fixableResult(status Level, detail, remediation string) Result {
	res := b.result(status, detail, remediation)
	res.Fixable = true
	return res
}

// interrupted is what a probe returns when its context is already done. It
// is a Warn, not a Fail: an interrupted doctor must not report a healthy
// machine as broken.
func (b baseProbe) interrupted(err error) Result {
	detail := "the check was interrupted before it finished"
	if err != nil {
		detail = "the check was interrupted before it finished: " + err.Error()
	}
	return b.result(Warn, detail, "Run `shellforge doctor` again.")
}

// fixAction is one reversible, non-privileged remediation Fix owns.
type fixAction struct {
	describe string // what Fix prints after performing it, past tense
	apply    func(ctx context.Context) error
}

// fixActions is the whole allowlist. It is a function returning a fresh map
// rather than a package-level var, because go-style forbids global mutable
// state, and a map var is mutable from any test in the package.
//
// disk_free is the only entry. Every other remediation this package can
// report needs elevation (installing WSL, enabling a Windows optional
// feature, updating the WSL kernel, starting the Docker daemon) and is
// printed for the learner to run themselves, never executed.
func fixActions() map[string]fixAction {
	return map[string]fixAction{
		"disk_free": {
			describe: "created the Shellforge data directory",
			apply: func(ctx context.Context) error {
				dir, err := platform.DataDir()
				if err != nil {
					return err
				}
				return platform.EnsureDir(dir)
			},
		},
	}
}

// newProbes builds the twelve probes in report order.
func newProbes(p SandboxProber, r runner) []Probe {
	return []Probe{
		osVersionProbe{baseProbe: baseProbe{id: "os_version", docAnchor: docAnchorOSTooOld}, r: r},
		cpuVirtualizationProbe{baseProbe: baseProbe{id: "cpu_virtualization", docAnchor: docAnchorVirtDisabled}, r: r},
		vmPlatformProbe{baseProbe: baseProbe{id: "vm_platform_feature", docAnchor: docAnchorVMPlatformMissing, windowsOnly: true}, r: r},
		wslInstalledProbe{baseProbe: baseProbe{id: "wsl_installed", docAnchor: docAnchorWSLNotInstalled, windowsOnly: true}, r: r},
		wslVersion2Probe{baseProbe: baseProbe{id: "wsl_version_2", docAnchor: docAnchorWSLVersion1, windowsOnly: true}, r: r},
		wslKernelProbe{baseProbe: baseProbe{id: "wsl_kernel_current", docAnchor: docAnchorWSLKernelOutdated, windowsOnly: true}, r: r},
		dockerPresentProbe{baseProbe: baseProbe{id: "docker_present", docAnchor: docAnchorDockerNotFound}, r: r},
		dockerDaemonProbe{baseProbe: baseProbe{id: "docker_daemon_running", docAnchor: docAnchorDockerDaemonDown}, r: r},
		diskFreeProbe{baseProbe: baseProbe{id: "disk_free", docAnchor: docAnchorDiskSpaceLow}},
		terminalVTProbe{baseProbe: baseProbe{id: "terminal_vt_support", docAnchor: docAnchorTerminalNoVT}},
		windowsTerminalProbe{baseProbe: baseProbe{id: "windows_terminal", docAnchor: docAnchorTerminalNotWT, windowsOnly: true}},
		sandboxHealthProbe{baseProbe: baseProbe{id: "sandbox_health", docAnchor: docAnchorSandboxUnhealthy}, prober: p},
	}
}

// applicableProbes filters newProbes by AppliesTo(goos). It takes goos as a
// parameter, never reading runtime.GOOS itself, so both CI legs test both
// lists.
func applicableProbes(goos string, p SandboxProber, r runner) []Probe {
	all := newProbes(p, r)
	out := make([]Probe, 0, len(all))
	for _, probe := range all {
		if probe.AppliesTo(goos) {
			out = append(out, probe)
		}
	}
	return out
}
