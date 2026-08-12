package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// The isolation test the destructive-safety skill requires: `rm -rf` inside a
// provisioned sandbox leaves the host untouched, and setup recovers.
//
// This is the automated form of the Day 1 exit criterion "rm -rf / inside the
// sandbox does nothing to the host". A manual run of `sudo rm -rf /*` does NOT
// establish it, because the container is created with --security-opt
// no-new-privileges and sudo is refused before the removal ever runs: that
// proves sudo is blocked, not that the filesystem is isolated. The removal has
// to actually execute for the criterion to mean anything.

// TestSandboxHasNoHostMounts asserts the container cannot see the host
// filesystem at all. This is the structural half of the promise, and it holds
// whether or not anything destructive ever runs.
func TestSandboxHasNoHostMounts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	sess := goldenSession(t, ctx)

	// The WSL automount paths must not exist. On the Docker backend they never
	// would, but asserting it here means the same expectation is already
	// written down when WslRuntime arrives on Day 3, where it is load bearing:
	// /mnt/c inside the sandbox is a direct route to the learner's real files.
	for _, p := range []string{"/mnt/c", "/mnt/d", "/mnt/wsl", "/host"} {
		res, err := sess.Exec(ctx, []string{"test", "-e", p}, runtime.ExecOpts{User: demoUser})
		if err != nil {
			t.Fatalf("probe %s: %v", p, err)
		}
		if res.ExitCode == 0 {
			t.Errorf("%s exists inside the sandbox, which is a path to the host filesystem", p)
		}
	}

	// And the container itself must carry no bind mount. Asking Docker
	// directly is stronger than probing paths, because it catches a mount at
	// a name nothing above thought to check.
	out, err := exec.CommandContext(ctx, "docker", "inspect",
		"--format", "{{range .Mounts}}{{.Type}}:{{.Source}}->{{.Destination}} {{end}}",
		"--", goldenSandboxName).Output()
	if err != nil {
		t.Fatalf("docker inspect: %v", err)
	}
	if mounts := strings.TrimSpace(string(out)); mounts != "" {
		t.Errorf("the sandbox container has host mounts, so the host filesystem is reachable from inside: %s", mounts)
	}
}

// TestDestructiveRemovalInsideTheSandboxCannotReachTheHost is the real
// isolation test. It runs a recursive delete inside the sandbox, as the
// learner, for real, and then proves two things: the host is untouched, and
// Setup puts the level back.
func TestDestructiveRemovalInsideTheSandboxCannotReachTheHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	sess := goldenSession(t, ctx)
	level := Demo()
	if err := level.Setup(ctx, sess); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// A canary on the HOST, in this test's own temp directory. If the sandbox
	// could reach the host filesystem, a recursive delete from / would be the
	// thing that found it. t.TempDir is removed by the test framework, so this
	// leaves nothing behind either way.
	canaryDir := t.TempDir()
	canaryPath := filepath.Join(canaryDir, "do-not-delete.txt")
	const canaryContent = "the host filesystem must survive this test"
	if err := os.WriteFile(canaryPath, []byte(canaryContent), 0o600); err != nil {
		t.Fatalf("write the host canary: %v", err)
	}

	// Record the host's own working directory listing too, so the assertion is
	// not limited to one file in one temp directory.
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	before, err := os.ReadDir(repoRoot)
	if err != nil {
		t.Fatalf("read the host working directory: %v", err)
	}

	// The real thing, as the learner, with no sudo. This deletes everything
	// the learner owns inside the container: their home, the level, and the
	// state directory. Root-owned paths such as /bin and /usr survive only
	// because the container drops CAP_DAC_OVERRIDE, which is why the session
	// is still usable afterwards.
	//
	// A leading `sudo` would prove nothing: no-new-privileges refuses it
	// before the removal runs at all.
	res, err := sess.Exec(ctx, []string{"/bin/bash", "-lc", "rm -rf / 2>/dev/null; rm -rf /* 2>/dev/null; rm -rf ~ 2>/dev/null; true"}, runtime.ExecOpts{
		User: demoUser,
		Env:  map[string]string{"HOME": "/home/learner"},
	})
	if err != nil {
		t.Fatalf("run the destructive removal: %v", err)
	}
	t.Logf("the destructive removal exited %d", res.ExitCode)

	// 1. The host canary is untouched, byte for byte.
	got, err := os.ReadFile(canaryPath)
	if err != nil {
		t.Fatalf("THE HOST CANARY IS GONE. A delete inside the sandbox reached the host filesystem: %v", err)
	}
	if string(got) != canaryContent {
		t.Fatalf("THE HOST CANARY WAS MODIFIED. want %q, got %q", canaryContent, got)
	}

	// 2. The host working directory still has everything it started with.
	after, err := os.ReadDir(repoRoot)
	if err != nil {
		t.Fatalf("THE HOST WORKING DIRECTORY IS GONE: %v", err)
	}
	if len(after) < len(before) {
		t.Errorf("the host working directory lost entries: had %d, now %d", len(before), len(after))
	}

	// 3. The level really was destroyed inside the sandbox. Without this the
	// test could pass because the removal silently did nothing at all, which
	// would make the first two assertions meaningless.
	gone, err := sess.Exec(ctx, []string{"test", "-e", level.ReportPath()}, runtime.ExecOpts{User: demoUser})
	if err != nil {
		t.Fatalf("probe the level root: %v", err)
	}
	logsGone, err := sess.Exec(ctx, []string{"test", "-e", filepath.ToSlash(level.Files[0].Path)}, runtime.ExecOpts{User: demoUser})
	if err != nil {
		t.Fatalf("probe a level asset: %v", err)
	}
	if gone.ExitCode == 0 && logsGone.ExitCode == 0 {
		t.Fatal("the removal deleted nothing inside the sandbox either, so this test proved nothing about isolation")
	}

	// 4. Setup recovers. This is what makes the promise usable rather than
	// merely true: a learner who wrecks their sandbox gets it back.
	if err := level.Setup(ctx, sess); err != nil {
		t.Fatalf("Setup could not recover the level after a destructive removal: %v", err)
	}
	if _, err := sess.Exec(ctx, []string{"/bin/bash", "-lc", level.Solution}, runtime.ExecOpts{
		User: demoUser,
		Env:  map[string]string{"HOME": "/home/learner"},
	}); err != nil {
		t.Fatalf("run the solution after recovery: %v", err)
	}
	passed, message, err := level.Check(ctx, sess)
	if err != nil {
		t.Fatalf("Check after recovery: %v", err)
	}
	if !passed {
		t.Errorf("the level does not pass after recovery: %s", message)
	}
}

// TestSudoIsRefusedByNoNewPrivileges records what the sandbox actually does
// today, and flags the conflict it creates.
//
// The image grants the learner passwordless sudo on purpose: its own comment
// says Act V teaches sudo. The container is then created with --security-opt
// no-new-privileges, which stops sudo from gaining privileges at all, so the
// grant has no effect and `sudo anything` fails with "the no new privileges
// flag is set".
//
// For the Day 1 gate this is a belt-and-braces win. For Act V it is a
// contradiction between the image and the runtime that has to be resolved
// before those levels can be authored. This test pins the current behaviour so
// the decision is made deliberately rather than discovered by a learner.
//
// TODO(v0.2): decide whether Act V drops no-new-privileges for levels that
// declare they need sudo, or whether the curriculum teaches sudo without
// running it. Do NOT simply remove the flag to make a level pass.
func TestSudoIsRefusedByNoNewPrivileges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	sess := goldenSession(t, ctx)

	res, err := sess.Exec(ctx, []string{"sudo", "-n", "id", "-u"}, runtime.ExecOpts{
		User:    demoUser,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("run sudo: %v", err)
	}
	if res.ExitCode == 0 && strings.TrimSpace(string(res.Stdout)) == "0" {
		t.Errorf("sudo succeeded and returned uid 0. That contradicts --security-opt no-new-privileges, " +
			"so either the flag was removed or the sandbox is more permissive than it was designed to be. " +
			"If this was deliberate, update the Containerfile comment and this test together.")
		return
	}
	t.Logf("sudo is refused, as the container configuration intends: exit %d, stderr %q",
		res.ExitCode, strings.TrimSpace(string(res.Stderr)))
}
