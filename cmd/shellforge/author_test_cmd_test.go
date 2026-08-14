package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// Everything in this file runs without a Docker daemon. It covers argument
// handling, the report format, and the hash-root refusals: the parts of the
// golden harness that are pure logic. The contract itself needs a real sandbox
// and lives in golden_test.go, gated.

// TestAuthorTestRefusesAllAndExplicitIdsTogether covers the one genuinely
// ambiguous invocation.
func TestAuthorTestRefusesAllAndExplicitIdsTogether(t *testing.T) {
	err := runAuthorTest(context.Background(), &bytes.Buffer{}, authorTestOptions{
		all: true,
		ids: []string{"nav-01"},
	})
	if err == nil {
		t.Fatal("accepted --all together with an explicit level id")
	}
	assertUserFacing(t, err)
}

// TestAuthorTestWithNoArgumentsListsTheLevels makes the usage error recoverable
// without reading the documentation, the same way the unknown-level message is.
func TestAuthorTestWithNoArgumentsListsTheLevels(t *testing.T) {
	err := runAuthorTest(context.Background(), &bytes.Buffer{}, authorTestOptions{})
	if err == nil {
		t.Fatal("accepted an invocation naming no levels and not passing --all")
	}
	assertUserFacing(t, err)

	var uxErr *ux.Error
	if !errors.As(err, &uxErr) {
		t.Fatalf("not a *ux.Error: %T", err)
	}
	for _, want := range []string{"nav-01", "pipe-05", "--all"} {
		if !strings.Contains(uxErr.Remediation, want) {
			t.Errorf("the remediation does not mention %q: %q", want, uxErr.Remediation)
		}
	}
}

// TestAuthorTestRefusesAnUnknownLevelBeforeProvisioning matters because
// provisioning is minutes. A typo must cost a second, not an image build.
func TestAuthorTestRefusesAnUnknownLevelBeforeProvisioning(t *testing.T) {
	err := runAuthorTest(context.Background(), &bytes.Buffer{}, authorTestOptions{
		ids: []string{"nav-01", "nav-99"},
	})
	if err == nil {
		t.Fatal("accepted an unknown level id")
	}
	assertUserFacing(t, err)

	var uxErr *ux.Error
	if errors.As(err, &uxErr) && uxErr.DocAnchor != docAnchorLevelNotFound {
		t.Errorf("DocAnchor = %q, want %q", uxErr.DocAnchor, docAnchorLevelNotFound)
	}
	if !strings.Contains(err.Error(), "nav-99") {
		t.Errorf("the error does not name the unknown id: %v", err)
	}
}

// TestAuthorTestRefusesAPackPathThatIsNotAPack covers --pack pointed at a file
// or at nothing.
func TestAuthorTestRefusesAPackPathThatIsNotAPack(t *testing.T) {
	for _, dir := range []string{"/nonexistent-pack-path", "go.mod"} {
		t.Run(dir, func(t *testing.T) {
			err := runAuthorTest(context.Background(), &bytes.Buffer{}, authorTestOptions{
				all: true, packDir: dir,
			})
			if err == nil {
				t.Fatalf("accepted %q as a pack directory", dir)
			}
			assertUserFacing(t, err)
		})
	}
}

// TestWriteGoldenReportTextForm pins the one-line-per-level shape, which mirrors
// `author validate` and is what a CI log shows.
func TestWriteGoldenReportTextForm(t *testing.T) {
	t.Run("a passing level names its timings", func(t *testing.T) {
		var buf bytes.Buffer
		err := writeGoldenReport(&buf, goldenReport{
			LevelID: "nav-01", OK: true,
			Timings: map[string]string{stageSetup: "600ms", stagePostCheck: "300ms"},
		}, false)
		if err != nil {
			t.Fatalf("writeGoldenReport: %v", err)
		}

		out := buf.String()
		if !strings.HasPrefix(out, "nav-01: ok") {
			t.Errorf("line does not start with the level and its verdict: %q", out)
		}
		for _, want := range []string{"setup 600ms", "post_check 300ms"} {
			if !strings.Contains(out, want) {
				t.Errorf("line is missing %q: %q", want, out)
			}
		}
	})

	t.Run("a failing level names the stage", func(t *testing.T) {
		var buf bytes.Buffer
		_ = writeGoldenReport(&buf, goldenReport{
			LevelID: "files-04", Stage: stagePreCheck,
			Problem: `objective "files-04-b" passed before the solution ran`,
		}, false)

		out := buf.String()
		for _, want := range []string{"files-04", stagePreCheck, "passed before the solution ran"} {
			if !strings.Contains(out, want) {
				t.Errorf("line is missing %q, so a CI log alone would not diagnose it: %q", want, out)
			}
		}
	})
}

// TestWriteGoldenReportJSONForm is one object per line, so a caller can stream it.
func TestWriteGoldenReportJSONForm(t *testing.T) {
	var buf bytes.Buffer
	_ = writeGoldenReport(&buf, goldenReport{
		LevelID: "pipe-05", Stage: stageSolution, Problem: "bash -lc exited 1",
		Timings: map[string]string{stageSetup: "1s"},
	}, true)

	var got goldenReport
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("the JSON form does not round trip: %v\n%s", err, buf.String())
	}
	if got.LevelID != "pipe-05" || got.Stage != stageSolution || got.OK {
		t.Errorf("round tripped to %+v", got)
	}
	if strings.Count(strings.TrimSpace(buf.String()), "\n") != 0 {
		t.Errorf("a report spans more than one line, so it cannot be streamed one per level:\n%s", buf.String())
	}
}

// TestFormatTimingsReportsStagesInRunOrder matters because the numbers are read
// as a sequence: setup, then the checks, then the solution.
func TestFormatTimingsReportsStagesInRunOrder(t *testing.T) {
	got := formatTimings(map[string]string{
		stageTeardown:  "5ms",
		stageSetup:     "1s",
		stagePostCheck: "300ms",
		stagePreCheck:  "200ms",
	})

	order := []string{stageSetup, stagePreCheck, stagePostCheck, stageTeardown}
	last := -1
	for _, stage := range order {
		at := strings.Index(got, stage)
		if at < 0 {
			t.Fatalf("timings omit %q: %s", stage, got)
		}
		if at < last {
			t.Errorf("stage %q appears out of run order: %s", stage, got)
		}
		last = at
	}
}

// TestFormatTimingsDoesNotDropAnUnknownStage keeps a future stage visible even if
// somebody forgets to add it to the ordering list.
func TestFormatTimingsDoesNotDropAnUnknownStage(t *testing.T) {
	got := formatTimings(map[string]string{stageSetup: "1s", "brand_new_stage": "9ms"})
	if !strings.Contains(got, "brand_new_stage 9ms") {
		t.Errorf("an unknown stage was silently dropped from the report: %s", got)
	}
}

// TestValidateHashRootRefusesUnsafeRoots is the refusal table the
// destructive-safety skill asks for on any path that resolves a root, even one
// that only reads.
//
// The walk cannot delete anything, so this is not protecting the learner's
// machine: it is protecting against a bug in the harness itself. A root that
// arrived empty or as "/" would hash the entire sandbox, which is slow enough to
// look like a hang and would make the purity comparison meaningless.
func TestValidateHashRootRefusesUnsafeRoots(t *testing.T) {
	refused := []struct {
		name, root string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"the whole filesystem", "/"},
		{"a relative dot", "."},
		{"a bare tilde, which nothing expands here", "~"},
		{"not absolute", "home/learner/quest"},
		{"a parent traversal", "/home/learner/../etc"},
		{"a parent traversal that cleans up", "/home/learner/quest/../.."},
		{"the learner home itself", "/home/learner"},
		{"the learner home with a trailing slash", "/home/learner/"},
		{"a sibling that shares the prefix", "/home/learnerX/quest"},
		{"a host path", "/etc/passwd"},
		{"another host path", "/usr/bin"},
	}

	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateHashRoot(tt.root); err == nil {
				t.Errorf("validateHashRoot(%q) was accepted, want a refusal", tt.root)
			}
		})
	}

	accepted := []string{
		"/home/learner/quest",
		"/home/learner/quest/",
		"/home/learner/work/nested/deeper",
	}
	for _, root := range accepted {
		t.Run("accepts "+root, func(t *testing.T) {
			if err := validateHashRoot(root); err != nil {
				t.Errorf("validateHashRoot(%q) was refused: %v", root, err)
			}
		})
	}
}

// TestAssertNoStrayProcessesComparesAgainstTheBaseline covers the diff logic
// without a container, including the case that matters: a process present before
// the level ran is not the level's fault.
func TestAssertNoStrayProcessesComparesAgainstTheBaseline(t *testing.T) {
	tests := []struct {
		name       string
		before     []string
		after      []string
		wantStray  bool
		wantNaming string
	}{
		{
			name:   "nothing changed",
			before: []string{"bash -l", "sleep 1000"},
			after:  []string{"bash -l", "sleep 1000"},
		},
		{
			name:   "a pre-existing process is not the level's fault",
			before: []string{"bash -l", "some-daemon"},
			after:  []string{"bash -l", "some-daemon"},
		},
		{
			name:       "a leaked process is reported",
			before:     []string{"bash -l"},
			after:      []string{"bash -l", "atlas-indexer --watch"},
			wantStray:  true,
			wantNaming: "atlas-indexer",
		},
		{
			name:   "a process that went away is not a failure",
			before: []string{"bash -l", "sleep 1000"},
			after:  []string{"bash -l"},
		},
		{
			name:       "a second copy of a baseline process is still a leak",
			before:     []string{"sleep 1000"},
			after:      []string{"sleep 1000", "sleep 1000"},
			wantStray:  true,
			wantNaming: "sleep 1000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := assertNoStrayProcesses(context.Background(), &goldenFakeSession{lines: tt.after}, tt.before)

			if tt.wantStray && got == "" {
				t.Fatal("a leaked process was not reported")
			}
			if !tt.wantStray && got != "" {
				t.Fatalf("reported a stray process when nothing leaked: %s", got)
			}
			if tt.wantNaming != "" && !strings.Contains(got, tt.wantNaming) {
				t.Errorf("the report does not name %q: %s", tt.wantNaming, got)
			}
		})
	}
}

// TestLearnerProcessesIgnoresItsOwnProbe keeps the snapshot from reporting the ps
// that produced it, which would make every level look like it leaked.
func TestLearnerProcessesIgnoresItsOwnProbe(t *testing.T) {
	sess := &goldenFakeSession{lines: []string{
		"bash -l",
		"ps -u learner -o args=",
		"sh -c set -e; find ...",
		"atlas-indexer",
	}}

	got, err := learnerProcesses(context.Background(), sess)
	if err != nil {
		t.Fatalf("learnerProcesses: %v", err)
	}

	for _, unwanted := range []string{"ps -u", "sh -c"} {
		for _, line := range got {
			if strings.Contains(line, unwanted) {
				t.Errorf("the snapshot includes its own machinery (%q): %v", unwanted, got)
			}
		}
	}
	if len(got) != 2 {
		t.Errorf("got %d processes, want 2 (bash and atlas-indexer): %v", len(got), got)
	}
}

// goldenFakeSession answers `ps` with a fixed process list and nothing else. It
// is separate from cmd_run_test.go's fakeSession because the harness assertions
// care only about ps output, and a shared fake would have to grow a mode switch.
type goldenFakeSession struct {
	lines []string
}

func (f *goldenFakeSession) Exec(_ context.Context, _ []string, _ runtime.ExecOpts) (runtime.ExecResult, error) {
	return runtime.ExecResult{ExitCode: 0, Stdout: []byte(strings.Join(f.lines, "\n") + "\n")}, nil
}

func (f *goldenFakeSession) Attach(context.Context, runtime.AttachOpts) (runtime.PTY, error) {
	panic("goldenFakeSession: Attach must never be called by the golden harness")
}

func (f *goldenFakeSession) PushFiles(context.Context, runtime.FileManifest) error { return nil }

func (f *goldenFakeSession) PullFile(context.Context, string) ([]byte, error) {
	panic("goldenFakeSession: PullFile must never be called by the golden harness")
}

func (f *goldenFakeSession) Close() error { return nil }
