package docker

import (
	"context"
	"os/exec"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/runtime/runtimetest"
)

// TestDockerContract wires the contract suite up against this
// implementation. The factory below provisions and destroys only
// runtimetest.SandboxName, never a production sandbox, per the warning at
// the top of runtimetest/contract.go, and skips cleanly when docker or its
// daemon is unavailable rather than failing, so this test is safe to run on
// any machine, including a CI runner with no Docker daemon.
func TestDockerContract(t *testing.T) {
	runtimetest.RunContract(t, dockerContractFactory)
}

func dockerContractFactory(t *testing.T) (rt runtime.Runtime, cleanup func()) {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("runtimetest: docker is not on PATH")
	}

	probe := exec.CommandContext(context.Background(), "docker", "version")
	if err := probe.Run(); err != nil {
		t.Skip("runtimetest: the Docker daemon is not answering (`docker version` failed)")
	}

	built, err := New(runtimetest.SandboxName, runtimetest.SandboxName)
	if err != nil {
		t.Fatalf("docker.New(%q, %q): %v", runtimetest.SandboxName, runtimetest.SandboxName, err)
	}
	return built, nil
}
