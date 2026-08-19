package wsl

import (
	"context"
	"os/exec"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/runtime/runtimetest"
)

// wslVersionProbeTimeout bounds the factory's `wsl.exe --version` probe, so
// a wedged WSL service is a clean skip rather than a hang: the docker
// sibling's factory has no equivalent wait at all, because `docker version`
// either answers or the daemon is not running, but wsl.exe can sit there
// indefinitely if the WSL service itself is stuck.
const wslVersionProbeTimeout = 5 * time.Second

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
// when this host is not Windows, when wsl.exe is not on PATH, when a short
// wsl.exe --version probe fails or hangs, or when no sandbox rootfs can be
// resolved. That makes this test safe to run on any machine, including this
// project's own Linux CI legs and a windows-latest GitHub runner with wsl.exe
// present but no WSL2 distribution or rootfs to import.
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

	// A short timeout, not context.Background(), because a wedged WSL
	// service must become a skip rather than a hang: without it, this probe
	// could block the whole test binary indefinitely on a host where the
	// WSL service itself is stuck rather than merely absent.
	probeCtx, cancel := context.WithTimeout(context.Background(), wslVersionProbeTimeout)
	defer cancel()
	probe := exec.CommandContext(probeCtx, "wsl.exe", "--version")
	if err := probe.Run(); err != nil {
		t.Skip("runtimetest: wsl.exe is not answering (`wsl.exe --version` failed or timed out); WSL2 is likely not installed")
	}

	// wsl.exe answering --version establishes only that the client binary
	// runs, not that this host can actually import and run a distribution:
	// GitHub's hosted windows-latest runners are exactly this case, with
	// wsl.exe present, --version succeeding, and no distribution or usable
	// WSL2 kernel underneath. The docker sibling's factory closes the same
	// gap by asking its daemon which OS its containers run rather than
	// merely whether it answers; this suite's equivalent question is
	// whether it has a rootfs to import at all, which is always false in a
	// plain `go test` job that never ran `make rootfs`, and is the same
	// honest signal a developer's own Windows 11 machine gives once a
	// rootfs is actually built.
	if _, err := defaultRootfs(); err != nil {
		t.Skip("runtimetest: no sandbox rootfs is available to import (run `make rootfs` first)")
	}

	built, err := New(runtimetest.SandboxName)
	if err != nil {
		t.Fatalf("wsl.New(%q): %v", runtimetest.SandboxName, err)
	}
	return built, nil
}
