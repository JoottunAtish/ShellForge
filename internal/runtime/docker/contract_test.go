package docker

import (
	"context"
	"os/exec"
	"strings"
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

	// Ask the daemon which OS its containers run, not merely whether it
	// answers. A Docker Desktop in Windows-container mode answers `docker
	// version` perfectly well and then cannot pull debian:bookworm-slim at
	// all, because a Linux-only base image has no Windows manifest. That is
	// this suite being unable to run, not the sandbox being broken, so it
	// skips for the same reason a missing daemon does. GitHub's hosted
	// windows-latest runners are exactly this case: Docker is present and
	// running, in Windows-container mode, with no Linux engine available.
	probe := exec.CommandContext(context.Background(), "docker", "version", "--format", "{{.Server.Os}}")
	out, err := probe.Output()
	if err != nil {
		t.Skip("runtimetest: the Docker daemon is not answering (`docker version` failed)")
	}
	if serverOS := strings.TrimSpace(string(out)); serverOS != "linux" {
		t.Skipf("runtimetest: the Docker daemon runs %s containers; the sandbox image is Linux only", serverOS)
	}

	built, err := New(runtimetest.SandboxName, runtimetest.SandboxName)
	if err != nil {
		t.Fatalf("docker.New(%q, %q): %v", runtimetest.SandboxName, runtimetest.SandboxName, err)
	}
	return built, nil
}
