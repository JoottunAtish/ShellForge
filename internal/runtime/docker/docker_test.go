package docker

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// fakeRunner records every argv it was asked to run and returns canned
// results in order. It never spawns a process, so the tests using it run
// with no Docker daemon and no docker binary on PATH.
type fakeRunner struct {
	results []fakeResult
	calls   [][]string
}

type fakeResult struct {
	stdout []byte
	stderr []byte
	code   int
	err    error
}

func (f *fakeRunner) run(_ context.Context, argv []string, _ []byte) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, append([]string(nil), argv...))
	if len(f.results) == 0 {
		return nil, nil, 0, nil
	}
	r := f.results[0]
	f.results = f.results[1:]
	return r.stdout, r.stderr, r.code, r.err
}

// TestNewRefusesBadIdentifiers is the refusal table: New must reject an
// empty name, a name with a space, a shell metacharacter, a leading dash,
// and a path separator, and must name the offending value in the error
// rather than accepting it sanitized. This needs no Docker daemon: New
// validates before it ever resolves the docker binary.
func TestNewRefusesBadIdentifiers(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"space", "sand box"},
		{"semicolon", "sandbox;rm"},
		{"pipe", "sandbox|rm"},
		{"command substitution", "sandbox$(rm)"},
		{"leading dash", "-f"},
		{"leading double dash", "--force"},
		{"path separator", "sand/box"},
	}

	for _, tc := range cases {
		t.Run("name "+tc.name, func(t *testing.T) {
			_, err := New(tc.value, "shellforge-sandbox")
			if err == nil {
				t.Fatalf("New(%q, ...) = nil error, want a refusal", tc.value)
			}
			if !bytes.Contains([]byte(err.Error()), []byte(tc.value)) {
				t.Errorf("New(%q, ...) error = %q, want it to name the offending value", tc.value, err.Error())
			}
		})

		t.Run("image "+tc.name, func(t *testing.T) {
			if tc.value == "" {
				// An empty image is also refused, but it cannot appear in its
				// own error message; that case is covered separately below.
				return
			}
			if tc.name == "path separator" {
				// "/" is a legitimate character in an image reference (it
				// separates a registry host from a repository path), so this
				// case does not apply to the image half of the table.
				t.Skip("a path separator is valid in an image reference")
			}
			_, err := New("shellforge-sandbox", tc.value)
			if err == nil {
				t.Fatalf("New(..., %q) = nil error, want a refusal", tc.value)
			}
			if !bytes.Contains([]byte(err.Error()), []byte(tc.value)) {
				t.Errorf("New(..., %q) error = %q, want it to name the offending value", tc.value, err.Error())
			}
		})
	}

	t.Run("empty image", func(t *testing.T) {
		if _, err := New("shellforge-sandbox", ""); err == nil {
			t.Fatal("New(..., \"\") = nil error, want a refusal")
		}
	})

	t.Run("valid image reference with registry, tag, and digest survives", func(t *testing.T) {
		for _, image := range []string{
			"shellforge-sandbox",
			"shellforge-sandbox:latest",
			"ghcr.io/joottunatish/shellforge-sandbox:v0.1",
			"ubuntu@sha256:abc123",
		} {
			if _, err := New("shellforge-sandbox", image); err != nil {
				t.Errorf("New(\"shellforge-sandbox\", %q) = %v, want a valid image reference to be accepted", image, err)
			}
		}
	})
}

// TestDestroyRefusesEmptyName asserts that an empty container name makes
// Destroy return an error without invoking docker at all. This is
// constructed directly rather than through New, because New itself already
// refuses an empty name; this test proves the guard inside Destroy holds
// independently, in case a future refactor ever constructs a dockerRuntime
// another way.
func TestDestroyRefusesEmptyName(t *testing.T) {
	fake := &fakeRunner{}
	rt := &dockerRuntime{name: "", image: "shellforge-sandbox", run: fake}

	if err := rt.Destroy(context.Background()); err == nil {
		t.Fatal("Destroy with an empty name = nil error, want a refusal")
	}
	if len(fake.calls) != 0 {
		t.Errorf("Destroy with an empty name invoked docker %d time(s), want zero: %v", len(fake.calls), fake.calls)
	}
}

// TestDestroyRefusesContainerWithoutMarker asserts that Destroy will not
// remove a container that exists but does not carry the shellforge.sandbox
// label: a name collision with something Shellforge did not create.
func TestDestroyRefusesContainerWithoutMarker(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stdout: []byte("true|<no value>"), code: 0}, // docker inspect: exists, running, no label
	}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	if err := rt.Destroy(context.Background()); err == nil {
		t.Fatal("Destroy of an unmarked container = nil error, want a refusal")
	}
	for _, call := range fake.calls {
		if len(call) >= 2 && call[1] == "rm" {
			t.Errorf("Destroy of an unmarked container ran %v, want no docker rm at all", call)
		}
	}
}

// TestProvisionArgvConstruction asserts the exact argv Provision produces
// for each docker invocation along its idempotent path: image missing,
// then built, then a container that does not exist yet, then started.
func TestProvisionArgvConstruction(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{code: 1}, // docker image inspect: miss
		{code: 0}, // docker build
		{stdout: []byte("false|<no value>"), code: 1}, // docker inspect (container): not found
		{code: 0}, // docker run
	}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	if err := rt.Provision(context.Background(), runtime.ImageSpec{Name: "shellforge-sandbox"}); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	containerfile, err := repoRootRelative(containerfilePath)
	if err != nil {
		t.Fatalf("repoRootRelative(%q): %v", containerfilePath, err)
	}
	buildContext, err := repoRootRelative(containerfileContext)
	if err != nil {
		t.Fatalf("repoRootRelative(%q): %v", containerfileContext, err)
	}

	want := [][]string{
		{"docker", "image", "inspect", "--", "shellforge-sandbox"},
		{"docker", "build", "-f", containerfile, "-t", "shellforge-sandbox", "--", buildContext},
		{"docker", "inspect", "--format", `{{.State.Running}}|{{index .Config.Labels "shellforge.sandbox"}}`, "--", "shellforge-sandbox"},
		{"docker", "run", "-d", "--name", "shellforge-sandbox", "--label", "shellforge.sandbox=1", "--network", "none", "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "FOWNER", "--security-opt", "no-new-privileges", "--", "shellforge-sandbox", "sleep", "infinity"},
	}
	assertArgvSequence(t, "Provision", fake.calls, want)
}

// TestDestroyArgvConstruction asserts the exact argv for a Destroy that
// finds a marked, running container and removes it.
func TestDestroyArgvConstruction(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stdout: []byte("true|1"), code: 0}, // docker inspect: exists, running, marked
		{code: 0},                           // docker rm -f
	}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	if err := rt.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	want := [][]string{
		{"docker", "inspect", "--format", `{{.State.Running}}|{{index .Config.Labels "shellforge.sandbox"}}`, "--", "shellforge-sandbox"},
		{"docker", "rm", "-f", "--", "shellforge-sandbox"},
	}
	assertArgvSequence(t, "Destroy", fake.calls, want)
}

// TestExecArgvConstruction asserts the exact argv Session.Exec produces,
// including the "--" terminator before the container name and command, and
// deterministic ordering of -e flags for a multi-entry Env map.
func TestExecArgvConstruction(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{{code: 0}}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}
	sess := &dockerSession{rt: rt, spec: runtime.SessionSpec{User: "learner", WorkDir: "/home/learner"}}

	_, err := sess.Exec(context.Background(), []string{"/bin/sh", "-c", "true"}, runtime.ExecOpts{
		User:    "root",
		WorkDir: "/home/learner/quest",
		Env:     map[string]string{"B": "2", "A": "1"},
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	want := [][]string{
		{"docker", "exec", "-u", "root", "-w", "/home/learner/quest", "-e", "A=1", "-e", "B=2", "--", "shellforge-sandbox", "/bin/sh", "-c", "true"},
	}
	assertArgvSequence(t, "Exec", fake.calls, want)
}

// TestExecUsesSessionDefaultsWhenOptsEmpty asserts that an empty
// ExecOpts.User and ExecOpts.WorkDir fall back to the session's, not to
// docker's own defaults, matching the interface doc comment on both fields.
func TestExecUsesSessionDefaultsWhenOptsEmpty(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{{code: 0}}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}
	sess := &dockerSession{rt: rt, spec: runtime.SessionSpec{User: "learner", WorkDir: "/home/learner"}}

	if _, err := sess.Exec(context.Background(), []string{"/bin/sh", "-c", "true"}, runtime.ExecOpts{}); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	want := [][]string{
		{"docker", "exec", "-u", "learner", "-w", "/home/learner", "--", "shellforge-sandbox", "/bin/sh", "-c", "true"},
	}
	assertArgvSequence(t, "Exec", fake.calls, want)
}

// TestExecRefusesBadWorkDir asserts that a WorkDir outside /home/learner is
// refused before it reaches an argv, with no docker invocation at all.
func TestExecRefusesBadWorkDir(t *testing.T) {
	fake := &fakeRunner{}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}
	sess := &dockerSession{rt: rt}

	if _, err := sess.Exec(context.Background(), []string{"/bin/sh", "-c", "true"}, runtime.ExecOpts{WorkDir: "/etc"}); err == nil {
		t.Fatal("Exec with WorkDir outside /home/learner = nil error, want a refusal")
	}
	if len(fake.calls) != 0 {
		t.Errorf("Exec with a refused WorkDir invoked docker %d time(s), want zero", len(fake.calls))
	}
}

// TestPullFileArgvConstruction asserts the exact argv PullFile produces.
func TestPullFileArgvConstruction(t *testing.T) {
	var tarBuf bytes.Buffer
	writeTarFile(t, &tarBuf, "home/learner/hello.txt", "hi\n")

	fake := &fakeRunner{results: []fakeResult{{stdout: tarBuf.Bytes(), code: 0}}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}
	sess := &dockerSession{rt: rt}

	got, err := sess.PullFile(context.Background(), "/home/learner/hello.txt")
	if err != nil {
		t.Fatalf("PullFile: %v", err)
	}
	if string(got) != "hi\n" {
		t.Errorf("PullFile content = %q, want %q", got, "hi\n")
	}

	want := [][]string{
		{"docker", "cp", "shellforge-sandbox:/home/learner/hello.txt", "-"},
	}
	assertArgvSequence(t, "PullFile", fake.calls, want)
}

// TestPullFileRefusesPathOutsideSandbox asserts PullFile refuses a path
// that does not resolve under /home/learner, with no docker invocation.
func TestPullFileRefusesPathOutsideSandbox(t *testing.T) {
	fake := &fakeRunner{}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}
	sess := &dockerSession{rt: rt}

	if _, err := sess.PullFile(context.Background(), "/etc/passwd"); err == nil {
		t.Fatal("PullFile(\"/etc/passwd\") = nil error, want a refusal")
	}
	if len(fake.calls) != 0 {
		t.Errorf("PullFile with a refused path invoked docker %d time(s), want zero", len(fake.calls))
	}
}

// TestPushFilesCleansUpStagingOnFailure asserts that a PushFiles failure
// (the docker cp itself failing) leaves no staging directory behind. It
// captures the staging path by inspecting the argv the fake runner
// recorded, since PushFiles does not expose it.
func TestPushFilesCleansUpStagingOnFailure(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{{code: 1, stderr: []byte("boom")}}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}
	sess := &dockerSession{rt: rt}

	err := sess.PushFiles(context.Background(), runtime.FileManifest{Files: []runtime.FileEntry{
		{Path: "/home/learner/quest/a.txt", Content: []byte("hi"), Mode: 0o644, Owner: "learner:learner"},
	}})
	if err == nil {
		t.Fatal("PushFiles with a failing docker cp = nil error, want the failure surfaced")
	}
	if len(fake.calls) != 1 {
		t.Fatalf("PushFiles ran %d docker invocation(s), want exactly 1 (the failing cp)", len(fake.calls))
	}

	stagingArg := fake.calls[0][2]
	stagingDir := stagingArg[:len(stagingArg)-2] // strip the trailing separator and "."
	assertPathAbsent(t, stagingDir)
}

// TestPushFilesRefusesBadOwner asserts that PushFiles refuses a
// FileEntry.Owner that does not match "user" or "user:group" before it
// stages anything or invokes docker.
func TestPushFilesRefusesBadOwner(t *testing.T) {
	fake := &fakeRunner{}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}
	sess := &dockerSession{rt: rt}

	err := sess.PushFiles(context.Background(), runtime.FileManifest{Files: []runtime.FileEntry{
		{Path: "/home/learner/quest/a.txt", Content: []byte("hi"), Mode: 0o644, Owner: "learner:learner;rm -rf /"},
	}})
	if err == nil {
		t.Fatal("PushFiles with a malformed owner = nil error, want a refusal")
	}
	if len(fake.calls) != 0 {
		t.Errorf("PushFiles with a refused owner invoked docker %d time(s), want zero", len(fake.calls))
	}
}

// TestStripCR is the CRLF unit test: it needs no Docker daemon because it
// exercises the pure function PushFiles calls before anything is staged.
func TestStripCR(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"crlf becomes lf", "#!/bin/sh\r\necho ok\r\n", "#!/bin/sh\necho ok\n"},
		{"already lf is untouched", "#!/bin/sh\necho ok\n", "#!/bin/sh\necho ok\n"},
		{"lone cr is untouched", "a\rb", "a\rb"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(stripCR([]byte(tc.in)))
			if got != tc.want {
				t.Errorf("stripCR(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestExecCancellationReturnsContextCanceled asserts that a context
// cancelled before the runner is even invoked is reported as
// context.Canceled, not as a generic error. This exercises the same
// classification path TestExecHonoursContextCancellation in the contract
// suite depends on, without needing a Docker daemon: the fake runner
// reports a generic failure, and Exec must still prefer ctx.Err() when the
// caller's own context is what carries the cancellation.
func TestExecCancellationReturnsContextCanceled(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{{code: -1, err: errors.New("boom")}}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}
	sess := &dockerSession{rt: rt}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := sess.Exec(ctx, []string{"/bin/sh", "-c", "true"}, runtime.ExecOpts{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Exec after cancellation: err = %v, want errors.Is(err, context.Canceled)", err)
	}
}

func assertArgvSequence(t *testing.T, op string, got, want [][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s ran %d docker invocation(s), want %d\ngot:  %v\nwant: %v", op, len(got), len(want), got, want)
	}
	for i := range want {
		if !equalArgv(got[i], want[i]) {
			t.Errorf("%s invocation %d:\ngot:  %v\nwant: %v", op, i, got[i], want[i])
		}
	}
}

func equalArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
