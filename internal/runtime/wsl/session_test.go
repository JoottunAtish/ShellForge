package wsl

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"testing"

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
