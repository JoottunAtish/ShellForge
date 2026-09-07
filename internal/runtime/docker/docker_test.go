package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
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
		{stdout: []byte(`true|<no value>|sha256:img|["bash","-c",""]`), code: 0}, // docker inspect: exists, running, no label
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
		{code: 1}, // docker inspect (container): not found
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
		{"docker", "inspect", "--format", containerInspectFormat, "--", "shellforge-sandbox"},
		{"docker", "run", "-d", "--name", "shellforge-sandbox", "--label", "shellforge.sandbox=1", "--network", "none", "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "FOWNER", "--security-opt", "no-new-privileges", "--", "shellforge-sandbox", "bash", "-c", sandboxInit},
	}
	assertArgvSequence(t, "Provision", fake.calls, want)
}

// currentImageID is the image id the fake reports for both the container and
// the tag in the reuse tests below, so the two agree and the container reads
// as current.
const currentImageID = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// inspectStdout builds the four-field `docker inspect` output for a container
// that exists, is running, and carries the marker label.
func inspectStdout(imageID string, cmd []string) []byte {
	encoded, err := json.Marshal(cmd)
	if err != nil {
		panic(err)
	}
	return []byte("true|1|" + imageID + "|" + string(encoded))
}

// wantRunArgv is the docker run every creation path must produce.
func wantRunArgv() []string {
	return append([]string{
		"docker", "run", "-d",
		"--name", "shellforge-sandbox",
		"--label", "shellforge.sandbox=1",
		"--network", "none",
		"--cap-drop", "ALL",
		"--cap-add", "CHOWN",
		"--cap-add", "FOWNER",
		"--security-opt", "no-new-privileges",
		"--", "shellforge-sandbox",
	}, sandboxCommand()...)
}

// TestProvisionReusesACurrentContainer asserts the cheap path: a container
// that is ours, running, built from the image the tag points at now, and
// started with the argv sandboxCommand produces now, is left alone. No
// removal and no re-creation, and no work beyond the two inspects it takes to
// establish all of that.
func TestProvisionReusesACurrentContainer(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{code: 0}, // docker image inspect: hit
		{stdout: inspectStdout(currentImageID, sandboxCommand()), code: 0}, // ours, running, current
		{stdout: []byte(currentImageID + "\n"), code: 0},                   // docker image inspect --format {{.Id}}
	}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	if err := rt.Provision(context.Background(), runtime.ImageSpec{Name: "shellforge-sandbox"}); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	want := [][]string{
		{"docker", "image", "inspect", "--", "shellforge-sandbox"},
		{"docker", "inspect", "--format", containerInspectFormat, "--", "shellforge-sandbox"},
		{"docker", "image", "inspect", "--format", "{{.Id}}", "--", "shellforge-sandbox"},
	}
	assertArgvSequence(t, "Provision", fake.calls, want)
}

// TestProvisionReplacesAContainerBuiltFromAnOlderImage is the bug the Day 5
// content run hit. `make image` rebuilds a tag in place, so a container
// created from the previous build keeps the old layers under the same name.
// Reusing it runs the learner against an image that predates the fix being
// tested, which is how a green run and a broken sandbox coexist.
func TestProvisionReplacesAContainerBuiltFromAnOlderImage(t *testing.T) {
	const olderImageID = "sha256:2222222222222222222222222222222222222222222222222222222222222222"

	fake := &fakeRunner{results: []fakeResult{
		{code: 0}, // docker image inspect: hit
		{stdout: inspectStdout(olderImageID, sandboxCommand()), code: 0}, // ours, but stale layers
		{stdout: []byte(currentImageID + "\n"), code: 0},                 // docker image inspect --format {{.Id}}
		{code: 0}, // docker rm -f
		{code: 0}, // docker run
	}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	if err := rt.Provision(context.Background(), runtime.ImageSpec{Name: "shellforge-sandbox"}); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	want := [][]string{
		{"docker", "image", "inspect", "--", "shellforge-sandbox"},
		{"docker", "inspect", "--format", containerInspectFormat, "--", "shellforge-sandbox"},
		{"docker", "image", "inspect", "--format", "{{.Id}}", "--", "shellforge-sandbox"},
		{"docker", "rm", "-f", "--", "shellforge-sandbox"},
		wantRunArgv(),
	}
	assertArgvSequence(t, "Provision", fake.calls, want)
}

// TestProvisionReplacesAContainerRunningAnOlderInit covers the other half:
// the image is current but PID 1 is not. A container started before
// sandboxInit learned to reap keeps that PID 1 for its whole life, so the
// zombies the new init exists to prevent come back under a binary that
// contains the fix.
//
// The argv is compared before the image id is asked for at all, so a stale
// init costs one docker call fewer than a stale image.
func TestProvisionReplacesAContainerRunningAnOlderInit(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{code: 0}, // docker image inspect: hit
		{stdout: inspectStdout(currentImageID, []string{"sleep", "infinity"}), code: 0}, // the PID 1 that never reaped
		{code: 0}, // docker rm -f
		{code: 0}, // docker run
	}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	if err := rt.Provision(context.Background(), runtime.ImageSpec{Name: "shellforge-sandbox"}); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	want := [][]string{
		{"docker", "image", "inspect", "--", "shellforge-sandbox"},
		{"docker", "inspect", "--format", containerInspectFormat, "--", "shellforge-sandbox"},
		{"docker", "rm", "-f", "--", "shellforge-sandbox"},
		wantRunArgv(),
	}
	assertArgvSequence(t, "Provision", fake.calls, want)
}

// TestProvisionNeverRemovesAnUnmarkedContainer is the refusal that guards
// every path above. Staleness is only ever assessed for a container carrying
// the Shellforge label, so a name collision with something a user built is
// refused rather than replaced. Getting that order wrong would turn a helpful
// rebuild into `docker rm -f` on somebody else's container.
func TestProvisionNeverRemovesAnUnmarkedContainer(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{code: 0}, // docker image inspect: hit
		{stdout: []byte("true|<no value>|" + currentImageID + `|["sleep","infinity"]`), code: 0},
	}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	if err := rt.Provision(context.Background(), runtime.ImageSpec{Name: "shellforge-sandbox"}); err == nil {
		t.Fatal("Provision over an unmarked container = nil error, want a refusal")
	}
	for _, call := range fake.calls {
		if len(call) >= 2 && (call[1] == "rm" || call[1] == "run") {
			t.Errorf("Provision over an unmarked container ran %v, want neither a rm nor a run", call)
		}
	}
}

// TestProvisionReplacesAContainerWithAnUnreadableCommand pins the fail closed
// direction. A container whose argv docker will not report as a JSON array of
// strings is not provably current, so it is replaced rather than trusted.
// Refusing to load instead would leave the learner with no way forward that a
// rebuild does not already provide.
func TestProvisionReplacesAContainerWithAnUnreadableCommand(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{code: 0}, // docker image inspect: hit
		{stdout: []byte("true|1|" + currentImageID + "|null"), code: 0},
		{code: 0}, // docker rm -f
		{code: 0}, // docker run
	}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	if err := rt.Provision(context.Background(), runtime.ImageSpec{Name: "shellforge-sandbox"}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if len(fake.calls) != 4 {
		t.Fatalf("Provision made %d docker calls, want 4 (image inspect, container inspect, rm, run): %v", len(fake.calls), fake.calls)
	}
	if !equalArgv(fake.calls[3], wantRunArgv()) {
		t.Errorf("run argv = %v, want %v", fake.calls[3], wantRunArgv())
	}
}

// TestDestroyArgvConstruction asserts the exact argv for a Destroy that
// finds a marked, running container and removes it.
func TestDestroyArgvConstruction(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stdout: []byte(`true|1|sha256:img|["bash","-c","x"]`), code: 0}, // docker inspect: exists, running, marked
		{code: 0}, // docker rm -f
	}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}

	if err := rt.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	want := [][]string{
		{"docker", "inspect", "--format", containerInspectFormat, "--", "shellforge-sandbox"},
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
		append([]string{"docker", "exec", "-u", "root", "-w", "/home/learner/quest", "-e", "A=1", "-e", "B=2", "--", "shellforge-sandbox"}, wrapWithPIDMarker([]string{"/bin/sh", "-c", "true"})...),
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
		append([]string{"docker", "exec", "-u", "learner", "-w", "/home/learner", "--", "shellforge-sandbox"}, wrapWithPIDMarker([]string{"/bin/sh", "-c", "true"})...),
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

// TestPushFilesStagesNothingOnTheHost asserts that PushFiles streams a tar
// on stdin rather than naming a host path, and that a failure therefore
// has no staging directory to leak. The ticket's test plan asks for a
// cleanup test; this is the stronger form of it, since there is nothing to
// clean up.
func TestPushFilesStagesNothingOnTheHost(t *testing.T) {
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
		t.Fatalf("PushFiles ran %d docker invocation(s), want exactly 1 (the failing cp): %v", len(fake.calls), fake.calls)
	}
	want := []string{"docker", "cp", "-", "shellforge-sandbox:/"}
	if !equalArgv(fake.calls[0], want) {
		t.Errorf("push argv = %v, want %v (a tar on stdin, never a host path)", fake.calls[0], want)
	}
}

// TestBuildPushTarOmitsSandboxRootAndAbove is the regression pin for the
// defect that broke this backend on Linux CI three times: a push archive
// that contains home/ or home/learner/ makes `docker cp` reassign those
// existing directories to the archive's ownership and mode, which locks
// the learner user out of its own home directory. Verified live at the
// time: /home went from learner:learner 0755 to 1001:1001 0750.
func TestBuildPushTarOmitsSandboxRootAndAbove(t *testing.T) {
	archive, dirs, err := buildPushTar([]pushEntry{
		{path: "/home/learner/contract/a/b/c.txt", content: []byte("hi"), mode: 0o644},
		{path: "/home/learner/top.txt", content: []byte("hi"), mode: 0o600},
	})
	if err != nil {
		t.Fatalf("buildPushTar: %v", err)
	}

	forbidden := map[string]bool{"home/": true, "home/learner/": true, "./": true, "/": true}
	var names []string
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading the push archive: %v", err)
		}
		names = append(names, hdr.Name)
		if forbidden[hdr.Name] {
			t.Errorf("push archive contains %q; copying it would reassign an existing directory the sandbox user depends on", hdr.Name)
		}
		if hdr.Uid != 0 || hdr.Gid != 0 {
			t.Errorf("push archive entry %q has uid/gid %d/%d, want 0/0 so the result does not depend on who ran the process", hdr.Name, hdr.Uid, hdr.Gid)
		}
	}

	wantNames := []string{
		"home/learner/contract/",
		"home/learner/contract/a/",
		"home/learner/contract/a/b/",
		"home/learner/contract/a/b/c.txt",
		"home/learner/top.txt",
	}
	if len(names) != len(wantNames) {
		t.Fatalf("push archive entries = %v, want exactly %v", names, wantNames)
	}
	for i := range wantNames {
		if names[i] != wantNames[i] {
			t.Errorf("push archive entry %d = %q, want %q (parents must precede their children)", i, names[i], wantNames[i])
		}
	}

	wantDirs := []string{"/home/learner/contract", "/home/learner/contract/a", "/home/learner/contract/a/b"}
	if len(dirs) != len(wantDirs) {
		t.Fatalf("reported dirs = %v, want %v", dirs, wantDirs)
	}
	for i := range wantDirs {
		if dirs[i] != wantDirs[i] {
			t.Errorf("reported dir %d = %q, want %q", i, dirs[i], wantDirs[i])
		}
	}
}

// TestBuildPushTarLaterEntryWins pins the FileManifest rule that entries
// are applied in order, which for a tar means the later entry at a given
// path must appear after the earlier one so extraction overwrites it.
func TestBuildPushTarLaterEntryWins(t *testing.T) {
	p := "/home/learner/contract/dup.txt"
	archive, _, err := buildPushTar([]pushEntry{
		{path: p, content: []byte("first\n"), mode: 0o644},
		{path: p, content: []byte("second\n"), mode: 0o644},
	})
	if err != nil {
		t.Fatalf("buildPushTar: %v", err)
	}

	var contents []string
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading the push archive: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("reading a push archive entry: %v", err)
		}
		contents = append(contents, string(body))
	}

	want := []string{"first\n", "second\n"}
	if len(contents) != len(want) {
		t.Fatalf("push archive file entries = %q, want %q", contents, want)
	}
	for i := range want {
		if contents[i] != want[i] {
			t.Errorf("push archive file entry %d = %q, want %q", i, contents[i], want[i])
		}
	}
}

// TestPushFilesChmodsAncestorDirectories asserts that the follow-up Exec
// PushFiles runs as root chmods every ancestor directory it created, up to
// but not including sandboxRoot, before it chmods and chowns the file
// itself, so the session user can traverse down to a pushed file rather
// than only root being able to reach it.
func TestPushFilesChmodsAncestorDirectories(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{{code: 0}, {code: 0}}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}
	sess := &dockerSession{rt: rt}

	err := sess.PushFiles(context.Background(), runtime.FileManifest{Files: []runtime.FileEntry{
		{Path: "/home/learner/contract/a/b/c.txt", Content: []byte("hi"), Mode: 0o644, Owner: "learner:learner"},
	}})
	if err != nil {
		t.Fatalf("PushFiles: %v", err)
	}

	if len(fake.calls) != 2 {
		t.Fatalf("PushFiles ran %d docker invocation(s), want 2 (cp, then apply): %v", len(fake.calls), fake.calls)
	}
	applyArgv := fake.calls[1]
	script := applyArgv[len(applyArgv)-1]

	wantOrder := []string{
		"chmod 0755 '/home/learner/contract' &&",
		"chmod 0755 '/home/learner/contract/a' &&",
		"chmod 0755 '/home/learner/contract/a/b' &&",
		"chmod 644 '/home/learner/contract/a/b/c.txt' &&",
		"chown 'learner:learner' '/home/learner/contract/a/b/c.txt' &&",
	}
	lastIdx := -1
	for _, want := range wantOrder {
		idx := strings.Index(script, want)
		if idx < 0 {
			t.Fatalf("apply script %q does not contain %q", script, want)
		}
		if idx < lastIdx {
			t.Fatalf("apply script %q has %q before an earlier required step", script, want)
		}
		lastIdx = idx
	}
	if strings.Contains(script, "chmod 0755 '/home/learner' ") || strings.Contains(script, "chmod 0755 '/home/learner' &&") {
		t.Errorf("apply script %q touches sandboxRoot itself, which PushFiles did not create and must not chmod", script)
	}
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
		// The replacement is a single non-overlapping pass, so a doubled
		// carriage return leaves one CRLF pair behind rather than being
		// fully normalized. Asserting the actual output locks in this known
		// gap, not a guarantee that the input is handled: a shebang shaped
		// this way still fails with "bad interpreter: /bin/sh^M" inside the
		// sandbox.
		{"a doubled carriage return before a crlf pair leaves one crlf pair behind: known gap, not a guarantee", "#!/bin/sh\r\r\necho ok\r\n", "#!/bin/sh\r\necho ok\n"},
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

// TestExecCancellationKillsSandboxProcessByPID asserts that when Exec's own
// invocation fails after the sandbox-side process announced its PID, Exec
// issues a follow-up `docker exec kill -9 <pid>` against that exact PID.
// This is the mechanism TestDockerContract/ExecHonoursContextCancellation's
// orphan probe depends on: killing the local docker client alone, which is
// all a cancelled context lets exec.CommandContext do, never reaches the
// process docker exec started inside the container.
func TestExecCancellationKillsSandboxProcessByPID(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{
		{stderr: []byte("SFPID:4242\n"), code: -1, err: errors.New("boom")}, // the cancelled Exec
		{code: 0}, // the follow-up kill
	}}
	rt := &dockerRuntime{name: "shellforge-sandbox", image: "shellforge-sandbox", run: fake}
	sess := &dockerSession{rt: rt}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := sess.Exec(ctx, []string{"/bin/sh", "-c", "true"}, runtime.ExecOpts{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Exec after cancellation: err = %v, want errors.Is(err, context.Canceled)", err)
	}

	if len(fake.calls) != 2 {
		t.Fatalf("Exec after cancellation ran %d docker invocation(s), want 2 (the exec, then the kill): %v", len(fake.calls), fake.calls)
	}
	want := []string{"docker", "exec", "--", "shellforge-sandbox", "kill", "-9", "4242"}
	if !equalArgv(fake.calls[1], want) {
		t.Errorf("follow-up kill argv = %v, want %v", fake.calls[1], want)
	}
}

// TestParseExecPIDMarker is the pure unit test for the marker
// wrapWithPIDMarker prepends to stderr: it needs no Docker daemon.
func TestParseExecPIDMarker(t *testing.T) {
	cases := []struct {
		name       string
		stderr     string
		wantPID    string
		wantStderr string
	}{
		{"marker with trailing output", "SFPID:123\nreal stderr", "123", "real stderr"},
		{"marker alone", "SFPID:7\n", "7", ""},
		{"no marker at all", "just some output", "", "just some output"},
		{"empty", "", "", ""},
		{"malformed pid is not numeric", "SFPID:12x3\nrest", "", "SFPID:12x3\nrest"},
		{"empty pid", "SFPID:\nrest", "", "SFPID:\nrest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pid, rest := parseExecPIDMarker([]byte(tc.stderr))
			if pid != tc.wantPID {
				t.Errorf("parseExecPIDMarker(%q) pid = %q, want %q", tc.stderr, pid, tc.wantPID)
			}
			if string(rest) != tc.wantStderr {
				t.Errorf("parseExecPIDMarker(%q) rest = %q, want %q", tc.stderr, rest, tc.wantStderr)
			}
		})
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
