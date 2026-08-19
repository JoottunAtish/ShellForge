package wsl

import (
	"context"
	"os/exec"
	goruntime "runtime"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/runtime/runtimetest"
)

// contractDistro must equal runtimetest.SandboxName: decision 3 in the
// Phase 1 plan is what lets the contract suite provision and destroy its
// own sandbox without either editing the suite or handing it a Runtime
// whose destroy constant is the production name. A rename of the suite
// constant becoming a test failure here, rather than silently breaking the
// destroy path, is the whole point of keeping two closed constants instead
// of one.
func TestContractDistroMatchesRuntimetestSandboxName(t *testing.T) {
	if contractDistro != runtimetest.SandboxName {
		t.Fatalf("contractDistro = %q, want it to equal runtimetest.SandboxName (%q)", contractDistro, runtimetest.SandboxName)
	}
}

// TestWslContract wires the contract suite up against this implementation.
// The factory below provisions and destroys only runtimetest.SandboxName,
// never the production sandbox, per the warning at the top of
// runtimetest/contract.go, and skips cleanly, before allocating anything,
// when this host is not Windows, when wsl.exe is not on PATH, or when a
// short wsl.exe --version probe fails. That makes this test safe to run on
// any machine, including this project's own Linux CI legs and a
// windows-latest GitHub runner with no WSL2.
func TestWslContract(t *testing.T) {
	runtimetest.RunContract(t, wslContractFactory)
}

func wslContractFactory(t *testing.T) (rt runtime.Runtime, cleanup func()) {
	t.Helper()

	if goruntime.GOOS != "windows" {
		t.Skip("runtimetest: the wsl backend only runs on Windows")
	}
	if _, err := exec.LookPath("wsl.exe"); err != nil {
		t.Skip("runtimetest: wsl.exe is not on PATH")
	}

	probe := exec.CommandContext(context.Background(), "wsl.exe", "--version")
	if err := probe.Run(); err != nil {
		t.Skip("runtimetest: wsl.exe is not answering (`wsl.exe --version` failed); WSL2 is likely not installed")
	}

	built, err := New(runtimetest.SandboxName)
	if err != nil {
		t.Fatalf("wsl.New(%q): %v", runtimetest.SandboxName, err)
	}
	return built, nil
}
