package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/runtime/wsl"
	"github.com/JoottunAtish/ShellForge/internal/sandbox"
)

// --------------------------------------------------------------------------
// status
// --------------------------------------------------------------------------

func TestSandboxStatusPrintsProvisionedRunningBackendAndDetail(t *testing.T) {
	rt := &fakeRuntime{status: runtime.Status{
		Provisioned: true,
		Running:     true,
		Backend:     "docker",
		Detail:      "shellforge-sandbox",
	}}
	fake := &fakeResolver{rt: rt}

	var out bytes.Buffer
	if err := runSandboxStatus(context.Background(), &out, fake.resolve, "auto"); err != nil {
		t.Fatalf("runSandboxStatus() error = %v, want nil", err)
	}

	got := out.String()
	for _, want := range []string{"true", "docker", "shellforge-sandbox"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q does not contain %q", got, want)
		}
	}
}

func TestSandboxStatusExitsNonZeroWhenNothingIsProvisioned(t *testing.T) {
	rt := &fakeRuntime{status: runtime.Status{}}
	fake := &fakeResolver{rt: rt}

	var out bytes.Buffer
	err := runSandboxStatus(context.Background(), &out, fake.resolve, "auto")
	if err == nil {
		t.Fatal("runSandboxStatus() error = nil, want a refusal for an unprovisioned sandbox")
	}

	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("error = %v (%T), want *ux.Error", err, err)
	}
	if uxErr.DocAnchor != anchorSandboxMissing {
		t.Errorf("DocAnchor = %q, want %q", uxErr.DocAnchor, anchorSandboxMissing)
	}
	if !strings.Contains(uxErr.Remediation, "shellforge init") {
		t.Errorf("remediation = %q, want it to name `shellforge init`", uxErr.Remediation)
	}
	if out.String() == "" {
		t.Error("output is empty; the state should still be reported alongside the error")
	}
}

// --------------------------------------------------------------------------
// destroy
// --------------------------------------------------------------------------

// newDockerDestroyFixture is the shared fixture for the destroy and rebuild
// refusal tests that do not need a real install directory: the Docker
// branch of sandbox.Plan touches no filesystem.
func newDockerDestroyFixture() (*fakeRuntime, *fakeResolver) {
	rt := &fakeRuntime{}
	choice := sandbox.Choice{Chosen: sandbox.Docker, Reason: "chose the docker backend"}
	return rt, &fakeResolver{rt: rt, choice: choice}
}

func TestSandboxDestroyPrintsThePlanIncludingTheAbsoluteDirectoryThenRequiresTheName(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	wantDir, err := wsl.InstallDir()
	if err != nil {
		t.Fatalf("wsl.InstallDir() error = %v", err)
	}

	rt := &fakeRuntime{}
	choice := sandbox.Choice{Chosen: sandbox.WSL, Reason: "chose the wsl backend"}
	fake := &fakeResolver{rt: rt, choice: choice}

	var out bytes.Buffer
	in := strings.NewReader("shellforge-sandbox\n")
	if err := runSandboxDestroy(context.Background(), in, &out, fake.resolve, false, "auto"); err != nil {
		t.Fatalf("runSandboxDestroy() error = %v, want nil", err)
	}

	got := out.String()
	if !strings.Contains(got, wantDir) {
		t.Errorf("output does not contain the absolute install directory %q: %q", wantDir, got)
	}
	if !strings.Contains(got, "shellforge-sandbox") {
		t.Errorf("output does not name the distribution: %q", got)
	}
	if !strings.Contains(got, "progress") || !strings.Contains(got, "not removed") {
		t.Errorf("output does not say the progress database is a separate step: %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "type") {
		t.Errorf("output does not prompt the learner to type anything: %q", got)
	}
	if rt.destroyCalls != 1 {
		t.Errorf("Destroy was called %d times, want 1", rt.destroyCalls)
	}
}

func TestSandboxDestroyWithADeclinedConfirmationNeverCallsDestroy(t *testing.T) {
	rt, fake := newDockerDestroyFixture()

	var out bytes.Buffer
	err := runSandboxDestroy(context.Background(), strings.NewReader("n\n"), &out, fake.resolve, false, "auto")
	if err == nil {
		t.Fatal("runSandboxDestroy() error = nil, want a refusal")
	}
	assertUserFacing(t, err)
	if rt.destroyCalls != 0 {
		t.Errorf("Destroy was called %d times, want 0", rt.destroyCalls)
	}
}

func TestSandboxDestroyWithAMistypedNameNeverCallsDestroy(t *testing.T) {
	for _, in := range []string{"shellforge", "Shellforge-Sandbox", "shellforge sandbox", " shellforge-sandbox x"} {
		t.Run(in, func(t *testing.T) {
			rt, fake := newDockerDestroyFixture()
			var out bytes.Buffer
			err := runSandboxDestroy(context.Background(), strings.NewReader(in+"\n"), &out, fake.resolve, false, "auto")
			if err == nil {
				t.Fatalf("runSandboxDestroy(%q) error = nil, want a refusal", in)
			}
			if rt.destroyCalls != 0 {
				t.Errorf("Destroy was called %d times for input %q, want 0", rt.destroyCalls, in)
			}
		})
	}
}

func TestSandboxDestroyWithEmptyInputNeverCallsDestroy(t *testing.T) {
	cases := map[string]string{"immediate EOF": "", "an empty line": "\n"}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			rt, fake := newDockerDestroyFixture()
			var out bytes.Buffer
			err := runSandboxDestroy(context.Background(), strings.NewReader(in), &out, fake.resolve, false, "auto")
			if err == nil {
				t.Fatalf("runSandboxDestroy() with %s: error = nil, want a refusal", name)
			}
			if rt.destroyCalls != 0 {
				t.Errorf("Destroy was called %d times, want 0", rt.destroyCalls)
			}
		})
	}
}

func TestSandboxDestroyWithYesCallsDestroyExactlyOnce(t *testing.T) {
	rt, fake := newDockerDestroyFixture()

	var out bytes.Buffer
	err := runSandboxDestroy(context.Background(), strings.NewReader(""), &out, fake.resolve, true, "auto")
	if err != nil {
		t.Fatalf("runSandboxDestroy() error = %v, want nil", err)
	}
	if rt.destroyCalls != 1 {
		t.Errorf("Destroy was called %d times, want 1", rt.destroyCalls)
	}

	got := out.String()
	if strings.Contains(got, "Type ") {
		t.Errorf("output contains the confirmation prompt even though --yes was set: %q", got)
	}
	if !strings.Contains(got, "container") {
		t.Errorf("output does not still print the removal plan: %q", got)
	}
}

func TestSandboxDestroyVerifiesRemovalAndReportsThePathToCheck(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	wantDir, err := wsl.InstallDir()
	if err != nil {
		t.Fatalf("wsl.InstallDir() error = %v", err)
	}

	t.Run("still reports provisioned after Destroy", func(t *testing.T) {
		rt := &fakeRuntime{status: runtime.Status{Provisioned: true}}
		choice := sandbox.Choice{Chosen: sandbox.WSL, Reason: "chose the wsl backend"}
		fake := &fakeResolver{rt: rt, choice: choice}

		var out bytes.Buffer
		err := runSandboxDestroy(context.Background(), strings.NewReader("shellforge-sandbox\n"), &out, fake.resolve, false, "auto")
		if err == nil {
			t.Fatal("runSandboxDestroy() error = nil, want a verification failure")
		}
		var uxErr *ux.Error
		if !errors.As(err, &uxErr) {
			t.Fatalf("error = %v (%T), want *ux.Error", err, err)
		}
		if uxErr.DocAnchor != anchorSandboxUnhealthy {
			t.Errorf("DocAnchor = %q, want %q", uxErr.DocAnchor, anchorSandboxUnhealthy)
		}
		if !strings.Contains(uxErr.Remediation, wantDir) {
			t.Errorf("remediation %q does not name the absolute path %q", uxErr.Remediation, wantDir)
		}
	})

	t.Run("Status errors after Destroy, so removal is not confirmed", func(t *testing.T) {
		rt := &fakeRuntime{statusErr: errors.New("docker daemon unreachable")}
		choice := sandbox.Choice{Chosen: sandbox.WSL, Reason: "chose the wsl backend"}
		fake := &fakeResolver{rt: rt, choice: choice}

		var out bytes.Buffer
		err := runSandboxDestroy(context.Background(), strings.NewReader("shellforge-sandbox\n"), &out, fake.resolve, false, "auto")
		if err == nil {
			t.Fatal("runSandboxDestroy() error = nil, want a verification failure when Status itself errors")
		}
		var uxErr *ux.Error
		if !errors.As(err, &uxErr) {
			t.Fatalf("error = %v (%T), want *ux.Error", err, err)
		}
		if uxErr.DocAnchor != anchorSandboxUnhealthy {
			t.Errorf("DocAnchor = %q, want %q", uxErr.DocAnchor, anchorSandboxUnhealthy)
		}
		if strings.Contains(out.String(), "Removed") {
			t.Errorf("output = %q, must not claim the sandbox was removed when it could not be verified", out.String())
		}
	})

	t.Run("really gone after Destroy", func(t *testing.T) {
		rt := &fakeRuntime{status: runtime.Status{Provisioned: false}}
		choice := sandbox.Choice{Chosen: sandbox.WSL, Reason: "chose the wsl backend"}
		fake := &fakeResolver{rt: rt, choice: choice}

		var out bytes.Buffer
		err := runSandboxDestroy(context.Background(), strings.NewReader("shellforge-sandbox\n"), &out, fake.resolve, false, "auto")
		if err != nil {
			t.Fatalf("runSandboxDestroy() error = %v, want nil", err)
		}
		if !strings.Contains(out.String(), wantDir) {
			t.Errorf("output does not print the path to check: %q", out.String())
		}
	})
}

// --------------------------------------------------------------------------
// rebuild
// --------------------------------------------------------------------------

func TestSandboxRebuildWithADeclinedConfirmationCallsNeitherDestroyNorProvision(t *testing.T) {
	rt, fake := newDockerDestroyFixture()

	var out bytes.Buffer
	err := runSandboxRebuild(context.Background(), strings.NewReader("n\n"), &out, fake.resolve, false, "auto")
	if err == nil {
		t.Fatal("runSandboxRebuild() error = nil, want a refusal")
	}
	if rt.destroyCalls != 0 {
		t.Errorf("Destroy was called %d times, want 0", rt.destroyCalls)
	}
	if rt.provisionCalls != 0 {
		t.Errorf("Provision was called %d times, want 0", rt.provisionCalls)
	}
}

func TestSandboxRebuildSaysWhatItWillDoThenDestroysThenProvisionsInThatOrder(t *testing.T) {
	var out bytes.Buffer
	rt := &fakeRuntime{out: &out}
	choice := sandbox.Choice{Chosen: sandbox.Docker, Reason: "chose the docker backend"}
	fake := &fakeResolver{rt: rt, choice: choice}

	err := runSandboxRebuild(context.Background(), strings.NewReader(""), &out, fake.resolve, true, "auto")
	if err != nil {
		t.Fatalf("runSandboxRebuild() error = %v, want nil", err)
	}

	if len(rt.callOrder) != 2 || rt.callOrder[0] != "Destroy" || rt.callOrder[1] != "Provision" {
		t.Fatalf("call order = %v, want [Destroy Provision]", rt.callOrder)
	}

	got := out.String()
	announceIdx := strings.Index(got, "rebuild")
	markerIdx := strings.Index(got, "fakeRuntime: Destroy called")
	if announceIdx == -1 {
		t.Fatalf("output does not announce the rebuild: %q", got)
	}
	if markerIdx == -1 {
		t.Fatalf("fakeRuntime never wrote its call marker: %q", got)
	}
	if announceIdx > markerIdx {
		t.Errorf("the announcement (index %d) comes after the first runtime call (index %d): %q", announceIdx, markerIdx, got)
	}
}

func TestSandboxRebuildRequiresTheSameConfirmationAsDestroy(t *testing.T) {
	inputs := []string{"shellforge-sandbox\n", "shellforge\n", "y\n", "", "\n"}
	for _, in := range inputs {
		t.Run(fmt.Sprintf("%q", in), func(t *testing.T) {
			rtD, fakeD := newDockerDestroyFixture()
			errD := runSandboxDestroy(context.Background(), strings.NewReader(in), io.Discard, fakeD.resolve, false, "auto")

			rtR, fakeR := newDockerDestroyFixture()
			errR := runSandboxRebuild(context.Background(), strings.NewReader(in), io.Discard, fakeR.resolve, false, "auto")

			acceptedD := errD == nil
			acceptedR := errR == nil
			if acceptedD != acceptedR {
				t.Errorf("input %q: destroy accepted=%v, rebuild accepted=%v, want them to match", in, acceptedD, acceptedR)
			}
			if acceptedD && rtD.destroyCalls != 1 {
				t.Errorf("input %q: destroy was accepted but Destroy was called %d times, want 1", in, rtD.destroyCalls)
			}
			if acceptedR && rtR.destroyCalls != 1 {
				t.Errorf("input %q: rebuild was accepted but Destroy was called %d times, want 1", in, rtR.destroyCalls)
			}
		})
	}
}

// --------------------------------------------------------------------------
// shell
// --------------------------------------------------------------------------

// TestSandboxShellChecksTheHostBeforeItResolvesOrProvisions matches
// TestCheckInteractiveShellSupported in cmd_run_test.go: the same host
// guard, exercised through `sandbox shell` instead of `run`.
func TestSandboxShellChecksTheHostBeforeItResolvesOrProvisions(t *testing.T) {
	fake := &fakeResolver{err: errors.New("no backend available in this fake")}

	err := runSandboxShell(context.Background(), fake.resolve, "auto")

	if goruntime.GOOS == "windows" {
		if err == nil {
			t.Fatal("runSandboxShell() error = nil, want a Windows refusal")
		}
		var uxErr *ux.Error
		if !errors.As(err, &uxErr) {
			t.Fatalf("error = %v (%T), want *ux.Error", err, err)
		}
		if uxErr.DocAnchor != anchorWindowsNeedsWSL {
			t.Errorf("DocAnchor = %q, want %q", uxErr.DocAnchor, anchorWindowsNeedsWSL)
		}
		if strings.Contains(uxErr.Remediation, "Docker") {
			t.Errorf("remediation reads as Docker-specific: %q", uxErr.Remediation)
		}
		if fake.calls != 0 {
			t.Errorf("resolve was called %d times, want 0: nothing should be provisioned on Windows", fake.calls)
		}
		return
	}

	if fake.calls != 1 {
		t.Errorf("resolve was called %d times, want 1", fake.calls)
	}
}

// --------------------------------------------------------------------------
// --runtime flag threading (BLOCKING 5): all four verbs used to hardcode
// sandbox.Auto, so `sandbox destroy` after `init --runtime=docker` on a
// machine where WSL2 is also available resolved WSL, removed nothing, and
// still reported success. Each verb must now parse its own --runtime flag
// with sandbox.ParseBackend, exactly as runInit already does, and pass the
// result to resolve.
// --------------------------------------------------------------------------

// sandboxVerbCase names one of the four sandbox verb functions, wrapped
// behind a single signature so one table can drive status, shell, destroy,
// and rebuild identically. Destroy and rebuild are given an empty stdin
// deliberately: with fake.err set, resolve fails before either function
// ever reads from it, so no confirmation prompt is reached.
type sandboxVerbCase struct {
	name string
	run  func(fake *fakeResolver, wantFlag string) error
}

func sandboxVerbCases() []sandboxVerbCase {
	return []sandboxVerbCase{
		{"status", func(fake *fakeResolver, wantFlag string) error {
			var out bytes.Buffer
			return runSandboxStatus(context.Background(), &out, fake.resolve, wantFlag)
		}},
		{"shell", func(fake *fakeResolver, wantFlag string) error {
			return runSandboxShell(context.Background(), fake.resolve, wantFlag)
		}},
		{"destroy", func(fake *fakeResolver, wantFlag string) error {
			var out bytes.Buffer
			return runSandboxDestroy(context.Background(), strings.NewReader(""), &out, fake.resolve, false, wantFlag)
		}},
		{"rebuild", func(fake *fakeResolver, wantFlag string) error {
			var out bytes.Buffer
			return runSandboxRebuild(context.Background(), strings.NewReader(""), &out, fake.resolve, false, wantFlag)
		}},
	}
}

// TestSandboxVerbsThreadRuntimeDockerThroughToResolve drives each verb with
// --runtime=docker and checks, through the recording fakeResolver, that
// Docker is what actually reached resolve. Before the fix this failed for
// every verb: each one passed sandbox.Auto regardless of what its own flag
// said, because there was no flag at all.
//
// shell is skipped on Windows: checkSandboxShellSupported refuses before
// --runtime is ever parsed there, for an unrelated, already-tested reason,
// so resolve is never called and fake.lastWant would still be its zero
// value.
func TestSandboxVerbsThreadRuntimeDockerThroughToResolve(t *testing.T) {
	for _, tc := range sandboxVerbCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "shell" && goruntime.GOOS == "windows" {
				t.Skip("shell refuses before resolve on Windows")
			}
			fake := &fakeResolver{err: errors.New("stop right after resolve")}
			_ = tc.run(fake, "docker")
			if fake.calls != 1 {
				t.Fatalf("resolve was called %d times, want exactly 1", fake.calls)
			}
			if fake.lastWant != sandbox.Docker {
				t.Errorf("resolve was called with want = %q, want %q", fake.lastWant, sandbox.Docker)
			}
		})
	}
}

// TestSandboxVerbsWithAnUnknownRuntimeFlagFailBeforeConstructingARuntime is
// the four-verb version of
// TestInitWithAnUnknownRuntimeFlagFailsBeforeConstructingARuntime: an
// unknown --runtime value must be refused through ux.Fail, and resolve
// must never be called, so nothing is constructed for a value nobody asked
// for.
func TestSandboxVerbsWithAnUnknownRuntimeFlagFailBeforeConstructingARuntime(t *testing.T) {
	for _, tc := range sandboxVerbCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "shell" && goruntime.GOOS == "windows" {
				t.Skip("shell refuses before resolve on Windows, for an unrelated reason")
			}
			fake := &fakeResolver{}
			err := tc.run(fake, "podman")
			if err == nil {
				t.Fatal("error = nil, want a refusal for an unknown --runtime value")
			}
			var uxErr *ux.Error
			if !errors.As(err, &uxErr) {
				t.Fatalf("error = %v (%T), want *ux.Error", err, err)
			}
			if fake.calls != 0 {
				t.Errorf("resolve was called %d times, want 0: nothing should be constructed for an unknown flag", fake.calls)
			}
		})
	}
}

// TestSandboxVerbsDefaultToAutoWithNoRuntimeFlag confirms the common case
// is unchanged: the flag's own default value is "auto", and each verb
// still resolves sandbox.Auto when it is left at that default.
func TestSandboxVerbsDefaultToAutoWithNoRuntimeFlag(t *testing.T) {
	for _, tc := range sandboxVerbCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "shell" && goruntime.GOOS == "windows" {
				t.Skip("shell refuses before resolve on Windows")
			}
			fake := &fakeResolver{err: errors.New("stop right after resolve")}
			_ = tc.run(fake, "auto")
			if fake.lastWant != sandbox.Auto {
				t.Errorf("resolve was called with want = %q, want %q", fake.lastWant, sandbox.Auto)
			}
		})
	}
}
