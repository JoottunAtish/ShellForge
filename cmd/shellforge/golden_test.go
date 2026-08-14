package main

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/runtime/docker"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// The golden test: every level in the pack through the contract in
// docs/LEVEL-FORMAT.md section 7, against a real container.
//
// Gated twice, and both gates are load bearing.
//
// A Linux Docker daemon has to be present, matching the probe every other
// Docker-dependent test in this repository uses. A Docker Desktop in
// Windows-container mode answers `docker version` happily and then cannot pull a
// Linux base image at all.
//
// SHELLFORGE_GOLDEN=1 has to be set as well, and that is not belt and braces.
// GitHub's ubuntu runner DOES have a working Docker daemon, so a daemon-only gate
// would make `go test ./...` in the fast Test job start building the sandbox
// image from the Containerfile, turning a job that takes seconds into one that
// takes many minutes. The env var keeps this test in the Sandbox image job, where
// the image is already built, and leaves it available to a developer who wants it
// locally.

// goldenTimeout bounds the whole sequence. The first run includes an image build.
const goldenTimeout = 25 * time.Minute

// requireGoldenSandbox skips unless both gates are satisfied.
func requireGoldenSandbox(t *testing.T) {
	t.Helper()

	if os.Getenv("SHELLFORGE_GOLDEN") != "1" {
		t.Skip("set SHELLFORGE_GOLDEN=1 to run the golden tests; they provision a container and build the sandbox image")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not on PATH")
	}
	probe := exec.CommandContext(context.Background(), "docker", "version", "--format", "{{.Server.Os}}")
	out, err := probe.Output()
	if err != nil {
		t.Skip("the Docker daemon is not answering (`docker version` failed)")
	}
	if serverOS := strings.TrimSpace(string(out)); serverOS != "linux" {
		t.Skipf("the Docker daemon runs %s containers; the sandbox image is Linux only", serverOS)
	}
}

// goldenSession provisions the author-test sandbox and returns a session on it.
func goldenSession(t *testing.T, ctx context.Context) runtime.Session {
	t.Helper()

	rt, err := docker.New(goldenSandboxName, goldenSandboxImage)
	if err != nil {
		t.Fatalf("construct the docker runtime: %v", err)
	}
	if err := rt.Provision(ctx, runtime.ImageSpec{Name: goldenSandboxImage}); err != nil {
		t.Fatalf("provision %s: %v", goldenSandboxName, err)
	}

	sess, err := rt.StartSession(ctx, runtime.SessionSpec{
		User:     sandboxUser,
		WorkDir:  sandboxHome,
		StateDir: setupStateDir(),
		Env:      map[string]string{"SF_STATE": setupStateDir()},
	})
	if err != nil {
		t.Fatalf("open a session: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// TestEveryLevelGoldenPath is the contract, over every level in the pack.
//
// Subtests are deliberately NOT parallel. There is one container, named by a
// compile-time constant, and two levels setting up in it at once would collide:
// they share /home/learner and the state directory, and each one's setup tears
// down before it builds.
func TestEveryLevelGoldenPath(t *testing.T) {
	requireGoldenSandbox(t)

	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	pack, err := content.Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}
	packFS, err := goldenPackFS()
	if err != nil {
		t.Fatalf("root the pack filesystem: %v", err)
	}

	harness := goldenHarness{
		sess:   goldenSession(t, ctx),
		packFS: packFS,
		engine: verify.NewEngine(),
	}

	order := levelOrder(pack)
	if len(order) == 0 {
		t.Fatal("the embedded pack has no levels")
	}

	for _, id := range order {
		level, ok := pack.Level(id)
		if !ok {
			t.Fatalf("Pack.Order named %q but Pack.Level does not have it", id)
		}

		t.Run(id, func(t *testing.T) {
			report := runGoldenLevel(ctx, harness, level)
			if !report.OK {
				t.Errorf("%s failed at stage %q:\n      %s\n      timings: %v",
					report.LevelID, report.Stage, report.Problem, report.Timings)
			}
		})
	}
}

// TestGoldenTestCoversEveryLevelInThePack asserts the loop above cannot quietly
// skip a level.
//
// It runs without a container, because it is about the pack rather than about the
// sandbox: campaign order has to name every loaded level. A level that fell out
// of pack.yaml's acts would otherwise be silently untested, which is the failure
// mode a per-level golden test exists to prevent.
func TestGoldenTestCoversEveryLevelInThePack(t *testing.T) {
	pack, err := content.Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}

	order := levelOrder(pack)
	if len(order) != len(pack.Levels) {
		t.Errorf("campaign order names %d levels but the pack has %d loaded; a level is missing from the golden run",
			len(order), len(pack.Levels))
	}

	named := map[string]bool{}
	for _, id := range order {
		named[id] = true
	}
	for i := range pack.Levels {
		if !named[pack.Levels[i].ID] {
			t.Errorf("level %q is loaded but not named in campaign order, so the golden test never runs it",
				pack.Levels[i].ID)
		}
	}
}

// TestPipe05RejectsNearMisses is the accepts-wrong-answer check for the one level
// whose answer is a number a learner could arrive at the wrong way.
//
// The testing skill asks for this on any level where a plausible mistake produces
// output that looks right. Each case writes a near miss into the level's world and
// asserts the checks still refuse it.
func TestPipe05RejectsNearMisses(t *testing.T) {
	requireGoldenSandbox(t)

	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	pack, err := content.Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}
	level, ok := pack.Level("pipe-05")
	if !ok {
		t.Fatal("pipe-05 is not in the embedded pack")
	}
	packFS, err := goldenPackFS()
	if err != nil {
		t.Fatalf("root the pack filesystem: %v", err)
	}

	sess := goldenSession(t, ctx)
	session, err := newGoldenGameSession(sess, packFS, level)
	if err != nil {
		t.Fatalf("build the level: %v", err)
	}

	if err := session.Setup(ctx); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		_ = session.Teardown(cleanupCtx)
	})

	nearMisses := []struct {
		name    string
		script  string
		explain string
	}{
		{
			name:    "the count off by one",
			script:  `printf '146\n' > ~/quest/report.txt`,
			explain: "a learner who missed a file, or counted with the wrong flag",
		},
		{
			name:    "the count off by one the other way",
			script:  `printf '148\n' > ~/quest/report.txt`,
			explain: "a learner who counted a header line",
		},
		{
			name:    "an empty report",
			script:  `: > ~/quest/report.txt`,
			explain: "a redirect that ran before the pipeline",
		},
		{
			name:    "the codes lowercase",
			script:  `printf 'e401\ne500\ne503\n' > ~/quest/codes.txt`,
			explain: "grep -o without -E, or a pattern that lowercased",
		},
		{
			name:    "the codes unsorted",
			script:  `printf 'E503\nE401\nE500\n' > ~/quest/codes.txt`,
			explain: "uniq without sort, which only collapses adjacent duplicates",
		},
		{
			name:    "the codes with duplicates",
			script:  `printf 'E401\nE401\nE500\nE503\n' > ~/quest/codes.txt`,
			explain: "sort without -u",
		},
		{
			name:    "a fourth code that is not in the logs",
			script:  `printf 'E401\nE404\nE500\nE503\n' > ~/quest/codes.txt`,
			explain: "a pattern that matched something else in the line",
		},
	}

	for _, tt := range nearMisses {
		t.Run(tt.name, func(t *testing.T) {
			// Start from the solved state, so only the near miss under test is
			// wrong and the other objective passes. That is what makes this a
			// test of the check rather than of the setup.
			if err := applySolution(ctx, sess, level); err != nil {
				t.Fatalf("apply the solution: %v", err)
			}
			if _, err := sess.Exec(ctx, []string{"bash", "-lc", tt.script}, runtime.ExecOpts{
				User: sandboxUser, WorkDir: level.Setup.Root, Timeout: 30 * time.Second,
			}); err != nil {
				t.Fatalf("write the near miss: %v", err)
			}

			res, err := session.Check(ctx)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if res.Passed {
				t.Errorf("pipe-05 accepted a wrong answer (%s: %s).\n"+
					"      The level accepts something it should not. Tighten the check, and confirm it "+
					"still accepts every legitimate way to reach the right answer.", tt.name, tt.explain)
			}
		})
	}
}

// newGoldenGameSession builds a game.Session for the near-miss test, which needs
// one directly rather than through runGoldenLevel.
func newGoldenGameSession(sess runtime.Session, packFS fs.FS, level *content.Level) (*game.Session, error) {
	return game.NewSession(game.Config{
		Level:    level,
		Sess:     sess,
		PackFS:   packFS,
		Verifier: verify.NewEngine(),
	})
}
