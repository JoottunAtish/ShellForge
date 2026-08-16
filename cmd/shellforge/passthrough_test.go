package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// These tests cover the CLI half of issue #74.
//
// ux.Render resolves with errors.As, which binds the OUTERMOST *ux.Error in
// the chain. Wrapping an error the runtime already classified therefore does
// not add context, it overwrites the better answer with a vaguer one. The
// symptom was a user whose Docker Desktop WSL integration was off being told
// to run `docker info` and sent to sandbox-unhealthy, while a specific
// classification sat unread one level down.

func TestFailUnlessAlreadyUserFacingPassesAClassifiedErrorThrough(t *testing.T) {
	inner := ux.Fail(
		"build the sandbox image",
		errors.New("docker build exited 1"),
		"Open Docker Desktop, go to Settings, Resources, WSL integration.",
		"docker-wsl-integration-off",
	)

	got := failUnlessAlreadyUserFacing(
		"provision the sandbox",
		inner,
		"Run `docker info` to confirm the daemon is running.",
		"sandbox-unhealthy",
	)

	var uxErr *ux.Error
	if !errors.As(got, &uxErr) {
		t.Fatalf("result is not a *ux.Error: %v", got)
	}
	if uxErr.Op != inner.Op {
		t.Errorf("Op = %q, want %q: the CLI replaced the backend's own description", uxErr.Op, inner.Op)
	}
	if uxErr.Remediation != inner.Remediation {
		t.Errorf("Remediation = %q, want %q: the specific remediation was replaced by the generic one", uxErr.Remediation, inner.Remediation)
	}
	if uxErr.DocAnchor != inner.DocAnchor {
		t.Errorf("DocAnchor = %q, want %q: the learner is sent to the wrong page", uxErr.DocAnchor, inner.DocAnchor)
	}
}

// A classified error is passed through, which must not weaken the
// non-negotiable-6 guarantee: whatever comes out still has to be renderable
// with a remediation.
func TestFailUnlessAlreadyUserFacingKeepsTheGuarantee(t *testing.T) {
	inner := ux.Fail("build the sandbox image", errors.New("boom"), "Turn WSL integration on.", "docker-wsl-integration-off")
	assertUserFacing(t, failUnlessAlreadyUserFacing("provision the sandbox", inner, "Run `docker info`.", "sandbox-unhealthy"))
}

// A classified error nested deeper than one level is still found, because
// errors.As walks the chain. This is the shape a real failure has: the
// backend's *ux.Error is wrapped by fmt.Errorf on the way out.
func TestFailUnlessAlreadyUserFacingFindsANestedClassification(t *testing.T) {
	inner := ux.Fail("build the sandbox image", errors.New("boom"), "Turn WSL integration on.", "docker-wsl-integration-off")
	wrapped := fmt.Errorf("provision: %w", inner)

	got := failUnlessAlreadyUserFacing("provision the sandbox", wrapped, "Run `docker info`.", "sandbox-unhealthy")

	var uxErr *ux.Error
	if !errors.As(got, &uxErr) {
		t.Fatalf("result is not a *ux.Error: %v", got)
	}
	if uxErr.DocAnchor != "docker-wsl-integration-off" {
		t.Errorf("DocAnchor = %q, want docker-wsl-integration-off", uxErr.DocAnchor)
	}
}

// The other half of the rule, and the reason this is a conditional
// pass-through rather than deleting the wrapping: an unclassified error must
// still be wrapped, or it reaches the terminal as a bare Go error.
func TestFailUnlessAlreadyUserFacingWrapsAnUnclassifiedError(t *testing.T) {
	got := failUnlessAlreadyUserFacing(
		"provision the sandbox",
		errors.New("some unexpected failure"),
		"Run `docker info` to confirm the daemon is running.",
		"sandbox-unhealthy",
	)

	assertUserFacing(t, got)

	var uxErr *ux.Error
	if !errors.As(got, &uxErr) {
		t.Fatalf("result is not a *ux.Error: %v", got)
	}
	if uxErr.Op != "provision the sandbox" {
		t.Errorf("Op = %q, want the passed-in op", uxErr.Op)
	}
	if uxErr.DocAnchor != "sandbox-unhealthy" {
		t.Errorf("DocAnchor = %q, want sandbox-unhealthy", uxErr.DocAnchor)
	}
	if uxErr.Remediation == "" {
		t.Error("remediation is empty, which non-negotiable 6 forbids")
	}
}

func TestFailUnlessAlreadyUserFacingReturnsNilForNil(t *testing.T) {
	if got := failUnlessAlreadyUserFacing("provision the sandbox", nil, "r", "a"); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}
