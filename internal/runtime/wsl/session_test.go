package wsl

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/creack/pty"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

func TestExecArgvConstruction(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{{stderr: []byte(execPIDMarkerPrefix + "123\n"), code: 0}}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}
	sess := &wslSession{rt: rt, spec: runtime.SessionSpec{User: "learner", WorkDir: "/home/learner"}}

	_, err := sess.Exec(context.Background(), []string{"/bin/sh", "-c", "true"}, runtime.ExecOpts{
		User:    "root",
		WorkDir: "/home/learner/quest",
		Env:     map[string]string{"B": "2", "A": "1"},
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	want := [][]string{
		append([]string{
			"wsl.exe", "-d", sandboxDistro, "-u", "root", "--exec", "/bin/sh", "-c", execWrapperScript, "--", "/home/learner/quest",
			"/usr/bin/env", "A=1", "B=2",
		}, "/bin/sh", "-c", "true"),
	}
	assertArgvSequence(t, "Exec", fake.calls, want)
}

func TestExecUsesSessionDefaultsWhenOptsEmpty(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{{code: 0}}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}
	sess := &wslSession{rt: rt, spec: runtime.SessionSpec{User: "learner", WorkDir: "/home/learner"}}

	if _, err := sess.Exec(context.Background(), []string{"/bin/sh", "-c", "true"}, runtime.ExecOpts{}); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	want := [][]string{
		{"wsl.exe", "-d", sandboxDistro, "-u", "learner", "--exec", "/bin/sh", "-c", execWrapperScript, "--", "/home/learner", "/bin/sh", "-c", "true"},
	}
	assertArgvSequence(t, "Exec", fake.calls, want)
}

func TestExecRefusesAWorkDirOutsideLearnerHome(t *testing.T) {
	fake := &fakeRunner{}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}
	sess := &wslSession{rt: rt}

	if _, err := sess.Exec(context.Background(), []string{"/bin/sh", "-c", "true"}, runtime.ExecOpts{WorkDir: "/etc"}); err == nil {
		t.Fatal("Exec with WorkDir outside /home/learner = nil error, want a refusal")
	}
	if len(fake.calls) != 0 {
		t.Errorf("Exec with a refused WorkDir invoked wsl.exe %d time(s), want zero", len(fake.calls))
	}
}

func TestExecRefusesAUserValidIdentifierRejects(t *testing.T) {
	fake := &fakeRunner{}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}
	sess := &wslSession{rt: rt}

	if _, err := sess.Exec(context.Background(), []string{"/bin/sh", "-c", "true"}, runtime.ExecOpts{User: "-f"}); err == nil {
		t.Fatal("Exec with User \"-f\" = nil error, want a refusal")
	}
	if len(fake.calls) != 0 {
		t.Errorf("Exec with a refused User invoked wsl.exe %d time(s), want zero", len(fake.calls))
	}
}

func TestExecReportsANonZeroExitAsAResult(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{{code: 3}}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}
	sess := &wslSession{rt: rt, spec: runtime.SessionSpec{User: "learner"}}

	res, err := sess.Exec(context.Background(), []string{"/bin/false"}, runtime.ExecOpts{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
}

func TestExecStripsThePIDMarkerFromStderr(t *testing.T) {
	t.Run("marker present", func(t *testing.T) {
		fake := &fakeRunner{results: []fakeResult{{stderr: []byte(execPIDMarkerPrefix + "42\nreal stderr")}}}
		rt := &wslRuntime{distro: sandboxDistro, run: fake}
		sess := &wslSession{rt: rt, spec: runtime.SessionSpec{User: "learner"}}

		res, err := sess.Exec(context.Background(), []string{"/bin/true"}, runtime.ExecOpts{})
		if err != nil {
			t.Fatalf("Exec: %v", err)
		}
		if string(res.Stderr) != "real stderr" {
			t.Errorf("Stderr = %q, want %q", res.Stderr, "real stderr")
		}
	})

	t.Run("no marker", func(t *testing.T) {
		fake := &fakeRunner{results: []fakeResult{{stderr: []byte("plain stderr, no marker")}}}
		rt := &wslRuntime{distro: sandboxDistro, run: fake}
		sess := &wslSession{rt: rt, spec: runtime.SessionSpec{User: "learner"}}

		res, err := sess.Exec(context.Background(), []string{"/bin/true"}, runtime.ExecOpts{})
		if err != nil {
			t.Fatalf("Exec: %v", err)
		}
		if string(res.Stderr) != "plain stderr, no marker" {
			t.Errorf("Stderr = %q, want it unchanged", res.Stderr)
		}
	})
}

// TestExecKillsTheSandboxProcessWithAContextDetachedFromCancellation covers
// killSandboxProcess's own context. Before this derived from
// context.Background() unconditionally, the kill's own wsl.exe invocation
// had no relationship to the caller's context at all; deriving it from ctx
// with context.WithoutCancel instead means it keeps ctx's values (nothing
// here uses any yet, but a future caller might) while a caller context that
// is already cancelled, exactly the situation that made Exec's own
// invocation fail in the first place, does not also cancel this cleanup
// call before it gets a chance to run.
func TestExecKillsTheSandboxProcessWithAContextDetachedFromCancellation(t *testing.T) {
	fake := &ctxCapturingFakeRunner{fakeRunner: fakeRunner{results: []fakeResult{
		{stderr: []byte(execPIDMarkerPrefix + "99\n"), err: errors.New("wsl.exe: broken pipe")},
		{code: 0}, // the kill invocation
	}}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}
	sess := &wslSession{rt: rt, spec: runtime.SessionSpec{User: "learner"}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Exec's own context is already cancelled by the time the kill runs

	if _, err := sess.Exec(ctx, []string{"/bin/true"}, runtime.ExecOpts{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Exec with a cancelled context = %v, want context.Canceled", err)
	}

	if len(fake.errsAtCallTime) != 2 {
		t.Fatalf("Exec ran %d wsl.exe invocation(s), want 2 (the exec attempt plus the kill): %v", len(fake.errsAtCallTime), fake.calls)
	}
	if err := fake.errsAtCallTime[1]; err != nil {
		t.Errorf("killSandboxProcess's own context.Err() at call time = %v, want nil: a cancelled caller context must not also cancel the cleanup call", err)
	}
	wantKill := []string{"wsl.exe", "-d", sandboxDistro, "-u", "root", "--exec", "/bin/kill", "-9", "99"}
	if !equalArgv(fake.calls[1], wantKill) {
		t.Errorf("kill argv = %v, want %v", fake.calls[1], wantKill)
	}
}

func TestAttachArgvConstruction(t *testing.T) {
	got := attachArgv(sandboxDistro, "learner", "", nil, defaultAttachCommand())
	want := []string{
		"wsl.exe", "-d", sandboxDistro, "-u", "learner", "--exec",
		"/bin/bash", "--rcfile", "/opt/shellforge/rc/instrument.bash", "-i",
	}
	if !equalArgv(got, want) {
		t.Errorf("attachArgv = %v, want %v", got, want)
	}
	for _, a := range got {
		if a == "--cd" {
			t.Errorf("attachArgv = %v, want no --cd flag", got)
		}
	}
}

// TestAttachStartErrorReportsWindowsPtyUnsupportedThroughUxFail covers the
// error path Attach cannot exercise on this host: creack/pty's pty.Start
// returns ErrUnsupported unconditionally on every Windows build, and
// Windows is the only host the WSL backend runs on, so before this a
// learner reaching a level here saw the bare Go error
// "wsl.exe --exec: unsupported" with no remediation and no doc anchor.
func TestAttachStartErrorReportsWindowsPtyUnsupportedThroughUxFail(t *testing.T) {
	err := attachStartError(pty.ErrUnsupported)
	if !hasDocAnchor(err, "windows-needs-wsl") {
		t.Errorf("attachStartError(pty.ErrUnsupported) = %v, want DocAnchor windows-needs-wsl", err)
	}
	if !errors.Is(err, pty.ErrUnsupported) {
		t.Errorf("attachStartError(pty.ErrUnsupported) = %v, want it to wrap pty.ErrUnsupported", err)
	}
}

// TestAttachStartErrorWrapsAnyOtherFailurePlainly asserts the
// windows-needs-wsl treatment is specific to pty.ErrUnsupported and does
// not swallow an unrelated pty.Start failure under the same remediation.
func TestAttachStartErrorWrapsAnyOtherFailurePlainly(t *testing.T) {
	other := errors.New("boom")
	err := attachStartError(other)
	if hasDocAnchor(err, "windows-needs-wsl") {
		t.Errorf("attachStartError(%v) = %v, want no windows-needs-wsl anchor for an unrelated failure", other, err)
	}
	if !errors.Is(err, other) {
		t.Errorf("attachStartError(%v) = %v, want it to wrap the original error", other, err)
	}
}

func TestAttachRefusesAFakeRunner(t *testing.T) {
	fake := &fakeRunner{}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}
	sess := &wslSession{rt: rt, spec: runtime.SessionSpec{User: "learner"}}

	if _, err := sess.Attach(context.Background(), runtime.AttachOpts{}); err == nil {
		t.Fatal("Attach against a fakeRunner = nil error, want a refusal")
	}
}

func TestPushFilesArgvConstruction(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{{code: 0}}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}
	sess := &wslSession{rt: rt}

	err := sess.PushFiles(context.Background(), runtime.FileManifest{Files: []runtime.FileEntry{
		{Path: "/home/learner/quest/a.txt", Content: []byte("a"), Mode: 0o644, Owner: "learner:learner"},
		{Path: "/home/learner/quest/b.txt", Content: []byte("b"), Mode: 0o644, Owner: "learner:learner"},
		{Path: "/home/learner/quest/sub/c.txt", Content: []byte("c"), Mode: 0o644, Owner: "learner:learner"},
	}})
	if err != nil {
		t.Fatalf("PushFiles: %v", err)
	}

	if len(fake.calls) != 1 {
		t.Fatalf("PushFiles ran %d wsl.exe invocation(s), want exactly 1: %v", len(fake.calls), fake.calls)
	}
	want := []string{"wsl.exe", "-d", sandboxDistro, "-u", "root", "--exec", "/bin/tar", "-xf", "-", "-C", "/"}
	if !equalArgv(fake.calls[0], want) {
		t.Errorf("push argv = %v, want %v", fake.calls[0], want)
	}
}

func TestPushFilesStagesOneTarForTheWholeManifest(t *testing.T) {
	var stdinCapture []byte
	fake := &capturingFakeRunner{fakeRunner: fakeRunner{results: []fakeResult{{code: 0}}}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}
	sess := &wslSession{rt: rt}

	err := sess.PushFiles(context.Background(), runtime.FileManifest{Files: []runtime.FileEntry{
		{Path: "/home/learner/quest/a.txt", Content: []byte("a"), Mode: 0o644, Owner: "learner:learner"},
		{Path: "/home/learner/quest/sub/dir/b.txt", Content: []byte("b"), Mode: 0o644, Owner: "learner:learner"},
	}})
	if err != nil {
		t.Fatalf("PushFiles: %v", err)
	}
	if len(fake.stdins) != 1 {
		t.Fatalf("PushFiles sent stdin to %d invocation(s), want exactly 1", len(fake.stdins))
	}
	stdinCapture = fake.stdins[0]

	var names []string
	tr := tar.NewReader(bytes.NewReader(stdinCapture))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading the push tar: %v", err)
		}
		names = append(names, hdr.Name)
		if hdr.Name == "home/" || hdr.Name == "home/learner/" || hdr.Name == "./" || hdr.Name == "/" {
			t.Errorf("push tar contains %q, want nothing at or above /home/learner", hdr.Name)
		}
		// Uid and Gid are the entire mechanism by which PushFiles honours
		// FileEntry.Owner in a single wsl.exe invocation: this package
		// deliberately skips the follow-up chown the docker sibling does.
		// A directory entry this package synthesizes on the way to a file
		// carries no FileEntry.Owner of its own, so it takes the tar
		// writer's zero value the same as every file entry here, which
		// happens to be root:root, matching what buildPushTar's own doc
		// comment says about not reassigning existing directories above
		// sandboxRoot.
		if hdr.Name == "home/learner/quest/a.txt" || hdr.Name == "home/learner/quest/sub/dir/b.txt" {
			if hdr.Uid != ownerIDs["learner"] || hdr.Gid != ownerIDs["learner"] {
				t.Errorf("push tar entry %q has Uid=%d Gid=%d, want %d, %d (learner:learner)", hdr.Name, hdr.Uid, hdr.Gid, ownerIDs["learner"], ownerIDs["learner"])
			}
		}
		if hdr.Name == "home/learner/quest/sub/dir/" {
			if hdr.Uid != 0 || hdr.Gid != 0 {
				t.Errorf("push tar directory entry %q has Uid=%d Gid=%d, want 0, 0 (root:root)", hdr.Name, hdr.Uid, hdr.Gid)
			}
		}
	}
	want := []string{
		"home/learner/quest/",
		"home/learner/quest/sub/",
		"home/learner/quest/sub/dir/",
		"home/learner/quest/a.txt",
		"home/learner/quest/sub/dir/b.txt",
	}
	if len(names) != len(want) {
		t.Fatalf("push tar entries = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("push tar entry %d = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestPushFilesStripsCarriageReturnsWhileBuildingTheTar(t *testing.T) {
	fake := &capturingFakeRunner{fakeRunner: fakeRunner{results: []fakeResult{{code: 0}}}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}
	sess := &wslSession{rt: rt}

	err := sess.PushFiles(context.Background(), runtime.FileManifest{Files: []runtime.FileEntry{
		{Path: "/home/learner/quest/script.sh", Content: []byte("#!/bin/sh\r\necho hi\r\n"), Mode: 0o755},
		{Path: "/home/learner/quest/lonecr.txt", Content: []byte("a\rb"), Mode: 0o644},
	}})
	if err != nil {
		t.Fatalf("PushFiles: %v", err)
	}

	tr := tar.NewReader(bytes.NewReader(fake.stdins[0]))
	contents := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading the push tar: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("reading tar entry %s: %v", hdr.Name, err)
		}
		contents[hdr.Name] = string(body)
	}

	if got := contents["home/learner/quest/script.sh"]; got != "#!/bin/sh\necho hi\n" {
		t.Errorf("script.sh content = %q, want CRLF stripped to LF", got)
	}
	if got := contents["home/learner/quest/lonecr.txt"]; got != "a\rb" {
		t.Errorf("lonecr.txt content = %q, want the lone CR left untouched", got)
	}
}

func TestPushFilesRefusesAPathOutsideLearnerHome(t *testing.T) {
	fake := &fakeRunner{}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}
	sess := &wslSession{rt: rt}

	err := sess.PushFiles(context.Background(), runtime.FileManifest{Files: []runtime.FileEntry{
		{Path: "/etc/passwd", Content: []byte("x"), Mode: 0o644},
	}})
	if err == nil {
		t.Fatal("PushFiles with a path outside /home/learner = nil error, want a refusal")
	}
	if len(fake.calls) != 0 {
		t.Errorf("PushFiles with a refused path invoked wsl.exe %d time(s), want zero", len(fake.calls))
	}
}

func TestPushFilesRefusesAnOwnerThatIsNotAnIdentifierPair(t *testing.T) {
	fake := &fakeRunner{}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}
	sess := &wslSession{rt: rt}

	err := sess.PushFiles(context.Background(), runtime.FileManifest{Files: []runtime.FileEntry{
		{Path: "/home/learner/quest/a.txt", Content: []byte("x"), Mode: 0o644, Owner: "root; rm -rf /"},
	}})
	if err == nil {
		t.Fatal("PushFiles with a malformed owner = nil error, want a refusal")
	}
	if len(fake.calls) != 0 {
		t.Errorf("PushFiles with a refused owner invoked wsl.exe %d time(s), want zero", len(fake.calls))
	}
}

func TestPullFileArgvConstruction(t *testing.T) {
	fake := &fakeRunner{results: []fakeResult{{stdout: []byte("hi\n"), code: 0}}}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}
	sess := &wslSession{rt: rt}

	got, err := sess.PullFile(context.Background(), "/home/learner/hello.txt")
	if err != nil {
		t.Fatalf("PullFile: %v", err)
	}
	if string(got) != "hi\n" {
		t.Errorf("PullFile content = %q, want %q", got, "hi\n")
	}
	want := [][]string{
		{"wsl.exe", "-d", sandboxDistro, "-u", "root", "--exec", "/bin/cat", "--", "/home/learner/hello.txt"},
	}
	assertArgvSequence(t, "PullFile", fake.calls, want)
}

func TestPullFileRefusesAPathOutsideLearnerHome(t *testing.T) {
	fake := &fakeRunner{}
	rt := &wslRuntime{distro: sandboxDistro, run: fake}
	sess := &wslSession{rt: rt}

	if _, err := sess.PullFile(context.Background(), "/etc/shadow"); err == nil {
		t.Fatal("PullFile(\"/etc/shadow\") = nil error, want a refusal")
	}
	if len(fake.calls) != 0 {
		t.Errorf("PullFile with a refused path invoked wsl.exe %d time(s), want zero", len(fake.calls))
	}
}

func TestStripCRLeavesALoneCarriageReturnUntouched(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"crlf becomes lf", "a\r\nb\r\n", "a\nb\n"},
		{"lone cr untouched", "a\rb", "a\rb"},
		{"already lf", "a\nb\n", "a\nb\n"},
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

// capturingFakeRunner extends fakeRunner to also record the stdin bytes of
// every invocation, so PushFiles's tar can be inspected.
type capturingFakeRunner struct {
	fakeRunner
	stdins [][]byte
}

func (f *capturingFakeRunner) run(ctx context.Context, argv []string, stdin []byte) ([]byte, []byte, int, error) {
	f.stdins = append(f.stdins, append([]byte(nil), stdin...))
	return f.fakeRunner.run(ctx, argv, stdin)
}

// ctxCapturingFakeRunner extends fakeRunner to also record, for every
// invocation, whatever ctx.Err() reports at the moment run is called. This
// is deliberately not the context value itself: a context this package
// derives with its own cancel func, such as killSandboxProcess's killCtx,
// is meant to be cancelled by its own deferred cancel() the instant the
// call using it returns, so asserting on the context object after the fact
// would just observe that ordinary cleanup rather than whether the call was
// already cancelled before it ever ran.
type ctxCapturingFakeRunner struct {
	fakeRunner
	errsAtCallTime []error
}

func (f *ctxCapturingFakeRunner) run(ctx context.Context, argv []string, stdin []byte) ([]byte, []byte, int, error) {
	f.errsAtCallTime = append(f.errsAtCallTime, ctx.Err())
	return f.fakeRunner.run(ctx, argv, stdin)
}
