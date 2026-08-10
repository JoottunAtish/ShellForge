package runtimetest

// THIS SUITE DESTROYS SANDBOXES. READ THIS BEFORE WRITING A FACTORY.
//
// TestDestroyThenStatusReportsMissing calls Runtime.Destroy. On the WSL
// backend, Destroy eventually means wsl --unregister, which is instant,
// irreversible, and has no undo. A Factory that hands this suite a
// Runtime pointed at the developer's real sandbox, or at any distribution
// the developer cares about, is a bug, and the consequence is somebody's
// Linux installation gone for good.
//
// A Factory must therefore build a Runtime scoped to SandboxName, which
// is a fixed test-only constant. Never the production sandbox name, never
// a name read from the environment, never a name derived from anything a
// test can influence.
//
// The suite defends itself as far as the interface allows: before every
// Destroy it writes a marker file and reads it back, so a Runtime that is
// not the one this test provisioned fails the marker check instead of
// being destroyed. That is a backstop, not permission to be careless. The
// name in the Factory is the real guard.
//
// Refusal tests for the deletion path itself do not live here. They live
// with the implementation that does the deleting, which is the only place
// the refused inputs exist.

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// SandboxName is the only sandbox name this suite ever provisions or
// destroys. Every Factory must scope its Runtime to it.
const SandboxName = "shellforge-contracttest"

const (
	contractUser     = "learner"
	contractHome     = "/home/learner"
	contractRoot     = "/home/learner/contract"
	contractStateDir = "/home/learner/.shellforge"
	contractTimeout  = 2 * time.Minute
)

// ImageSpec returns the spec every contract assertion provisions with. An
// empty Reference means the backend provisions its own default image; the
// suite has no way to name a backend specific reference and must not learn
// one.
func ImageSpec() runtime.ImageSpec { return runtime.ImageSpec{Name: SandboxName} }

// Factory builds a Runtime under test, and returns a cleanup function.
//
// It is called once per contract assertion, so each assertion gets its own
// Runtime and a failure cannot cascade. An implementation whose
// prerequisites are unavailable (no Docker daemon, no WSL) must call
// t.Skip with a reason a human can act on, rather than failing, so the
// suite is usable on a machine that has only one backend.
//
// Skip BEFORE building anything. t.Skip unwinds the calling goroutine, so
// a Factory that skips after allocating leaks what it allocated, and the
// cleanup it was about to return is never registered. For the same reason
// a Factory must call t.Skip on the goroutine it was called on, never
// from a goroutine it started.
//
// cleanup may be nil when there is nothing to release. When it is not nil
// the suite registers it with t.Cleanup, so it runs even when an
// assertion calls t.Fatal.
type Factory func(t *testing.T) (rt runtime.Runtime, cleanup func())

func newRuntime(t *testing.T, f Factory) runtime.Runtime {
	t.Helper()
	rt, cleanup := f(t)
	if cleanup != nil {
		t.Cleanup(cleanup)
	}
	if rt == nil {
		t.Fatal("runtimetest: Factory returned a nil Runtime without calling t.Skip")
	}
	return rt
}

func contractContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), contractTimeout)
	t.Cleanup(cancel)
	return ctx
}

// provisionedSession provisions the contract sandbox and opens a session on
// it with networking off, which is the default the product ships.
func provisionedSession(ctx context.Context, t *testing.T, rt runtime.Runtime) runtime.Session {
	t.Helper()
	if err := rt.Provision(ctx, ImageSpec()); err != nil {
		t.Fatalf("Provision(%q): %v", SandboxName, err)
	}
	sess, err := rt.StartSession(ctx, runtime.SessionSpec{
		User:     contractUser,
		WorkDir:  contractHome,
		StateDir: contractStateDir,
		// Networking is deliberately left false.
	})
	if err != nil {
		t.Fatalf("StartSession on %q: %v", SandboxName, err)
	}
	t.Cleanup(func() { _ = sess.Close() }) // Close is documented as safe to call twice.
	return sess
}

// TestProvisionIsIdempotent asserts that calling Provision twice with the
// same spec succeeds both times and leaves a sandbox a session can still be
// started against and used.
//
// The interface has no way to enumerate sandboxes, so "leaves one sandbox"
// is asserted here only as "the same sandbox still works after a second
// Provision call". A backend-local test may additionally count containers
// or distributions named SandboxName; that is out of scope for this
// package.
func TestProvisionIsIdempotent(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	ctx := contractContext(t)

	if err := rt.Provision(ctx, ImageSpec()); err != nil {
		t.Fatalf("first Provision(%q): %v", SandboxName, err)
	}
	if err := rt.Provision(ctx, ImageSpec()); err != nil {
		t.Fatalf("second Provision(%q): %v", SandboxName, err)
	}

	status, err := rt.Status(ctx)
	if err != nil {
		t.Fatalf("Status after two Provision calls: %v", err)
	}
	if !status.Provisioned {
		t.Fatalf("Status after two Provision calls: Provisioned = false, want true")
	}
	if !status.Running {
		t.Fatalf("Status after two Provision calls: Running = false, want true")
	}

	sess, err := rt.StartSession(ctx, runtime.SessionSpec{
		User:     contractUser,
		WorkDir:  contractHome,
		StateDir: contractStateDir,
	})
	if err != nil {
		t.Fatalf("StartSession after two Provision calls: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	res, err := sess.Exec(ctx, []string{"/bin/sh", "-c", "true"}, runtime.ExecOpts{})
	if err != nil {
		t.Fatalf("Exec after two Provision calls: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("Exec exit code after two Provision calls: want 0, got %d", res.ExitCode)
	}
}

// TestStatusReportsProvisioned asserts the Provisioned, Running, and Backend
// fields of Status before and after Provision, and that StartSession before
// Provision fails with ErrSandboxMissing. Backend is asserted non-empty
// only: status.go documents it as display only, and no decision here may
// branch on its value.
func TestStatusReportsProvisioned(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	ctx := contractContext(t)

	before, err := rt.Status(ctx)
	if err != nil {
		t.Fatalf("Status before Provision: %v", err)
	}
	if before.Provisioned {
		t.Errorf("Status before Provision: Provisioned = true, want false")
	}

	if _, err := rt.StartSession(ctx, runtime.SessionSpec{User: contractUser, WorkDir: contractHome}); err == nil {
		t.Errorf("StartSession before Provision: want an error wrapping ErrSandboxMissing, got nil")
	} else if !errors.Is(err, runtime.ErrSandboxMissing) {
		t.Errorf("StartSession before Provision: err = %v, want errors.Is(err, ErrSandboxMissing)", err)
	}

	if err := rt.Provision(ctx, ImageSpec()); err != nil {
		t.Fatalf("Provision(%q): %v", SandboxName, err)
	}

	after, err := rt.Status(ctx)
	if err != nil {
		t.Fatalf("Status after Provision: %v", err)
	}
	if !after.Provisioned {
		t.Errorf("Status after Provision: Provisioned = false, want true")
	}
	if !after.Running {
		t.Errorf("Status after Provision: Running = false, want true")
	}
	if after.Backend == "" {
		t.Errorf("Status after Provision: Backend is empty, want a non-empty display string")
	}
}

// TestExecCapturesStreamsSeparately runs one command that writes distinct
// text to stdout and stderr, and asserts each stream contains only what was
// written to it.
func TestExecCapturesStreamsSeparately(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	ctx := contractContext(t)
	sess := provisionedSession(ctx, t, rt)

	res, err := sess.Exec(ctx, []string{"/bin/sh", "-c", "printf shellforge-stdout; printf shellforge-stderr >&2"}, runtime.ExecOpts{})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.TimedOut {
		t.Fatalf("Exec: TimedOut = true, want false")
	}
	if res.ExitCode != 0 {
		t.Fatalf("Exec exit code: want 0, got %d", res.ExitCode)
	}

	if !bytes.Contains(res.Stdout, []byte("shellforge-stdout")) {
		t.Errorf("stdout does not contain the text written to stdout: %q", res.Stdout)
	}
	if bytes.Contains(res.Stdout, []byte("shellforge-stderr")) {
		t.Errorf("stdout contains text written to stderr: %q", res.Stdout)
	}
	if !bytes.Contains(res.Stderr, []byte("shellforge-stderr")) {
		t.Errorf("stderr does not contain the text written to stderr: %q", res.Stderr)
	}
	if bytes.Contains(res.Stderr, []byte("shellforge-stdout")) {
		t.Errorf("stderr contains text written to stdout: %q", res.Stderr)
	}
}

// TestExecReportsExitCode asserts that a non-zero exit code is reported as
// its actual value, and that a successful invocation never returns an
// error. The err == nil assertion runs first in each case so the failure
// message names the conflation if it happens.
//
// A missing binary is deliberately not one of the cases here. Docker and
// WSL may genuinely differ on that result. TODO(v0.2): decide once a second
// backend makes the right answer observable rather than guessed.
func TestExecReportsExitCode(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	ctx := contractContext(t)
	sess := provisionedSession(ctx, t, rt)

	cases := []struct {
		name string
		code int
	}{
		{"zero", 0},
		{"one", 1},
		{"forty two", 42},
		{"one hundred and one", 101},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			res, err := sess.Exec(ctx, []string{"/bin/sh", "-c", "exit " + strconv.Itoa(tc.code)}, runtime.ExecOpts{})
			if err != nil {
				t.Fatalf("Exec returned an error for exit %d: %v", tc.code, err)
			}
			if res.ExitCode != tc.code {
				t.Errorf("Exec exit code: want %d, got %d", tc.code, res.ExitCode)
			}
			if res.TimedOut {
				t.Errorf("Exec: TimedOut = true, want false")
			}
		})
	}
}

// TestExecHonoursContextCancellation asserts two independent things: a
// cancelled context aborts a long-running Exec and leaves no orphan process
// inside the sandbox, and ExecOpts.Timeout expiry is reported as a result
// (TimedOut true, ExitCode -1, err nil), never as an error.
//
// The orphan check proves only that the sandbox-side process is gone from
// /proc inside the sandbox. A leaked host-side client process is invisible
// through this interface and is the backend's own problem to test.
func TestExecHonoursContextCancellation(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	base := contractContext(t)
	sess := provisionedSession(base, t, rt)

	t.Run("cancel aborts", func(t *testing.T) {
		pidPath := contractHome + "/.contract-pid"
		execCtx, cancel := context.WithCancel(base)

		errCh := make(chan error, 1)
		go func() {
			_, err := sess.Exec(execCtx, []string{"/bin/sh", "-c", "echo $$ > " + pidPath + "; exec sleep 30"}, runtime.ExecOpts{})
			errCh <- err
		}()

		deadline := time.Now().Add(2 * time.Second)
		var pid []byte
		for time.Now().Before(deadline) {
			got, err := sess.PullFile(base, pidPath)
			if err == nil && len(bytes.TrimSpace(got)) > 0 {
				pid = got
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(pid) == 0 {
			t.Fatal("the sandbox-side process never wrote its pid within 2 seconds")
		}

		cancel()

		select {
		case err := <-errCh:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Exec error after cancellation: %v, want errors.Is(err, context.Canceled)", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Exec did not return within 10 seconds of context cancellation")
		}

		probe, err := sess.Exec(base, []string{"/bin/sh", "-c", "test -d /proc/$(cat " + pidPath + ")"}, runtime.ExecOpts{})
		if err != nil {
			t.Fatalf("orphan probe Exec: %v", err)
		}
		if probe.ExitCode == 0 {
			t.Errorf("orphan probe: /proc/<pid> still exists after cancellation, the sandbox-side process was not reaped")
		}
	})

	t.Run("timeout is reported as TimedOut", func(t *testing.T) {
		res, err := sess.Exec(base, []string{"/bin/sh", "-c", "sleep 30"}, runtime.ExecOpts{Timeout: time.Second})
		if err != nil {
			t.Fatalf("Exec with a Timeout: %v, want nil", err)
		}
		if !res.TimedOut {
			t.Errorf("Exec with a Timeout: TimedOut = false, want true")
		}
		if res.ExitCode != -1 {
			t.Errorf("Exec with a Timeout: ExitCode = %d, want -1", res.ExitCode)
		}
	})
}

// TestExecRunsAsRequestedUser asserts that Exec with an empty User runs as
// the session user, and, when the backend supports it, that an explicit
// User is honoured too.
func TestExecRunsAsRequestedUser(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	ctx := contractContext(t)
	sess := provisionedSession(ctx, t, rt)

	t.Run("session user", func(t *testing.T) {
		res, err := sess.Exec(ctx, []string{"/bin/sh", "-c", "id -un"}, runtime.ExecOpts{})
		if err != nil {
			t.Fatalf("Exec: %v", err)
		}
		if got := strings.TrimSpace(string(res.Stdout)); got != contractUser {
			t.Errorf("id -un with the session user: want %q, got %q", contractUser, got)
		}
	})

	t.Run("explicit root", func(t *testing.T) {
		if !rt.Capabilities().MultiUser {
			t.Skip("runtimetest: backend reports Caps().MultiUser false")
		}
		res, err := sess.Exec(ctx, []string{"/bin/sh", "-c", "id -un"}, runtime.ExecOpts{User: "root"})
		if err != nil {
			t.Fatalf("Exec with User \"root\": %v", err)
		}
		if got := strings.TrimSpace(string(res.Stdout)); got != "root" {
			t.Errorf("id -un with User \"root\": want %q, got %q", "root", got)
		}
	})
}

// pushAndVerify pushes a single file and asserts its content, mode, and
// owner, all read back through the Session interface.
func pushAndVerify(ctx context.Context, t *testing.T, sess runtime.Session, path, content string, mode fs.FileMode, wantMode string) {
	t.Helper()
	err := sess.PushFiles(ctx, runtime.FileManifest{Files: []runtime.FileEntry{
		{Path: path, Content: []byte(content), Mode: mode, Owner: contractUser + ":" + contractUser},
	}})
	if err != nil {
		t.Fatalf("PushFiles(%s): %v", path, err)
	}

	got, err := sess.PullFile(ctx, path)
	if err != nil {
		t.Fatalf("PullFile(%s): %v", path, err)
	}
	if string(got) != content {
		t.Errorf("PullFile(%s) content: got %q, want %q", path, got, content)
	}

	res, err := sess.Exec(ctx, []string{"/bin/sh", "-c", "stat -c '%a %U:%G' " + path}, runtime.ExecOpts{})
	if err != nil {
		t.Fatalf("stat Exec for %s: %v", path, err)
	}
	want := wantMode + " " + contractUser + ":" + contractUser
	if got := strings.TrimSpace(string(res.Stdout)); got != want {
		t.Errorf("stat -c mode and owner of %s: got %q, want %q", path, got, want)
	}
}

// TestPushFilesMaterializesContentModeOwner asserts that PushFiles applies
// content, mode, and owner, creates parent directories as needed, and that
// when two entries target the same path the later one wins, matching the
// "applied in order" clause in FileManifest's doc comment.
func TestPushFilesMaterializesContentModeOwner(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	ctx := contractContext(t)
	sess := provisionedSession(ctx, t, rt)

	t.Run("executable script 0755", func(t *testing.T) {
		pushAndVerify(ctx, t, sess, contractRoot+"/exec-0755.sh", "#!/bin/sh\necho ok\n", 0755, "755")
	})

	t.Run("plain file 0644", func(t *testing.T) {
		pushAndVerify(ctx, t, sess, contractRoot+"/plain-0644.txt", "plain\n", 0644, "644")
	})

	t.Run("nested parents are created", func(t *testing.T) {
		pushAndVerify(ctx, t, sess, contractRoot+"/a/b/c.txt", "nested\n", 0644, "644")
	})

	t.Run("later entry wins", func(t *testing.T) {
		path := contractRoot + "/overwritten.txt"
		err := sess.PushFiles(ctx, runtime.FileManifest{Files: []runtime.FileEntry{
			{Path: path, Content: []byte("first\n"), Mode: 0644, Owner: contractUser + ":" + contractUser},
			{Path: path, Content: []byte("second\n"), Mode: 0644, Owner: contractUser + ":" + contractUser},
		}})
		if err != nil {
			t.Fatalf("PushFiles: %v", err)
		}
		got, err := sess.PullFile(ctx, path)
		if err != nil {
			t.Fatalf("PullFile(%s): %v", path, err)
		}
		if string(got) != "second\n" {
			t.Errorf("PushFiles with two entries at the same path: content = %q, want %q (the later entry)", got, "second\n")
		}
	})
}

// TestPushFilesStripsCarriageReturns asserts that PushFiles strips carriage
// returns from CRLF content, leaves LF-only content byte-identical, and
// that the stripped result actually runs as a script. That last case is the
// real-world proof that "bad interpreter: /bin/bash^M" cannot happen.
//
// TODO(v0.2): a lone CR with no LF, and binary content, are unspecified. See
// contract decision 9: freezing that rule now, to satisfy a test, would risk
// PushFiles silently corrupting a binary level asset.
func TestPushFilesStripsCarriageReturns(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	ctx := contractContext(t)
	sess := provisionedSession(ctx, t, rt)

	t.Run("crlf script becomes lf", func(t *testing.T) {
		path := contractRoot + "/crlf.sh"
		pushed := "#!/bin/sh\r\necho ok\r\n"
		want := "#!/bin/sh\necho ok\n"

		if err := sess.PushFiles(ctx, runtime.FileManifest{Files: []runtime.FileEntry{
			{Path: path, Content: []byte(pushed), Mode: 0755, Owner: contractUser + ":" + contractUser},
		}}); err != nil {
			t.Fatalf("PushFiles(%s): %v", path, err)
		}

		got, err := sess.PullFile(ctx, path)
		if err != nil {
			t.Fatalf("PullFile(%s): %v", path, err)
		}
		if string(got) != want {
			t.Errorf("PushFiles did not strip carriage returns: got %q, want %q", got, want)
		}
		if bytes.IndexByte(got, '\r') != -1 {
			t.Errorf("PushFiles left a carriage return in %s: %q", path, got)
		}
	})

	t.Run("already lf is untouched", func(t *testing.T) {
		path := contractRoot + "/lf-only.sh"
		content := "#!/bin/sh\necho ok\n"

		if err := sess.PushFiles(ctx, runtime.FileManifest{Files: []runtime.FileEntry{
			{Path: path, Content: []byte(content), Mode: 0755, Owner: contractUser + ":" + contractUser},
		}}); err != nil {
			t.Fatalf("PushFiles(%s): %v", path, err)
		}

		got, err := sess.PullFile(ctx, path)
		if err != nil {
			t.Fatalf("PullFile(%s): %v", path, err)
		}
		if string(got) != content {
			t.Errorf("PushFiles altered LF-only content: got %q, want %q (byte-identical)", got, content)
		}
	})

	t.Run("crlf script actually runs", func(t *testing.T) {
		path := contractRoot + "/crlf-runs.sh"
		content := "#!/bin/sh\r\nexit 0\r\n"

		if err := sess.PushFiles(ctx, runtime.FileManifest{Files: []runtime.FileEntry{
			{Path: path, Content: []byte(content), Mode: 0755, Owner: contractUser + ":" + contractUser},
		}}); err != nil {
			t.Fatalf("PushFiles(%s): %v", path, err)
		}

		res, err := sess.Exec(ctx, []string{path}, runtime.ExecOpts{})
		if err != nil {
			t.Fatalf("Exec(%s): %v", path, err)
		}
		if res.ExitCode != 0 {
			t.Errorf("Exec(%s) exit code: want 0, got %d (a CRLF shebang produces bad interpreter)", path, res.ExitCode)
		}
	})
}

// sanitizeName turns a table case name into something safe to use as a path
// component inside the sandbox.
func sanitizeName(name string) string {
	r := strings.NewReplacer(" ", "-", ",", "", "'", "")
	return r.Replace(name)
}

// TestPullFileRoundTrips asserts that a range of content round trips
// exactly, that reading a missing path is an error, and that two reads in a
// row do not change the file, discharging the "does not modify the sandbox"
// clause in PullFile's doc comment.
func TestPullFileRoundTrips(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	ctx := contractContext(t)
	sess := provisionedSession(ctx, t, rt)

	cases := []struct {
		name    string
		content string
	}{
		{"ascii", "hello, shellforge"},
		{"utf-8 multibyte", "shellforge: cafe au lait, konnichiwa こんにちは"},
		{"empty file", ""},
		{"no trailing newline", "no newline at the end"},
		{"128 KiB", strings.Repeat("x", 128*1024)},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			path := contractRoot + "/roundtrip-" + sanitizeName(tc.name) + ".txt"
			if err := sess.PushFiles(ctx, runtime.FileManifest{Files: []runtime.FileEntry{
				{Path: path, Content: []byte(tc.content), Mode: 0644, Owner: contractUser + ":" + contractUser},
			}}); err != nil {
				t.Fatalf("PushFiles(%s): %v", path, err)
			}
			got, err := sess.PullFile(ctx, path)
			if err != nil {
				t.Fatalf("PullFile(%s): %v", path, err)
			}
			if string(got) != tc.content {
				t.Errorf("PullFile(%s): content did not round trip", path)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		if _, err := sess.PullFile(ctx, contractRoot+"/does-not-exist.txt"); err == nil {
			t.Error("PullFile of a nonexistent path: want a non-nil error, got nil")
		}
	})

	t.Run("two reads do not mutate", func(t *testing.T) {
		path := contractRoot + "/stable.txt"
		if err := sess.PushFiles(ctx, runtime.FileManifest{Files: []runtime.FileEntry{
			{Path: path, Content: []byte("stable\n"), Mode: 0644, Owner: contractUser + ":" + contractUser},
		}}); err != nil {
			t.Fatalf("PushFiles(%s): %v", path, err)
		}

		statCmd := []string{"/bin/sh", "-c", "stat -c '%s %Y' " + path}
		before, err := sess.Exec(ctx, statCmd, runtime.ExecOpts{})
		if err != nil {
			t.Fatalf("stat before PullFile: %v", err)
		}
		if _, err := sess.PullFile(ctx, path); err != nil {
			t.Fatalf("first PullFile(%s): %v", path, err)
		}
		if _, err := sess.PullFile(ctx, path); err != nil {
			t.Fatalf("second PullFile(%s): %v", path, err)
		}
		after, err := sess.Exec(ctx, statCmd, runtime.ExecOpts{})
		if err != nil {
			t.Fatalf("stat after PullFile: %v", err)
		}
		if string(before.Stdout) != string(after.Stdout) {
			t.Errorf("stat output changed after two PullFile calls: before %q, after %q", before.Stdout, after.Stdout)
		}
	})
}

// TestDestroyThenStatusReportsMissing is the safety-critical assertion. It
// writes a marker naming this sandbox and this test, reads it back, and
// refuses to call Destroy unless the marker matches: a Runtime that is not
// the one this test provisioned fails the marker check instead of being
// destroyed. See the file-level comment for why this matters.
func TestDestroyThenStatusReportsMissing(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	ctx := contractContext(t)
	sess := provisionedSession(ctx, t, rt)

	markerPath := contractHome + "/.contract-marker"
	want := SandboxName + "/" + t.Name()
	if err := sess.PushFiles(ctx, runtime.FileManifest{Files: []runtime.FileEntry{
		{Path: markerPath, Content: []byte(want), Mode: 0644, Owner: contractUser + ":" + contractUser},
	}}); err != nil {
		t.Fatalf("PushFiles(%s): %v", markerPath, err)
	}

	got, err := sess.PullFile(ctx, markerPath)
	if err != nil {
		t.Fatalf("PullFile(%s): %v", markerPath, err)
	}
	if string(got) != want {
		t.Fatalf("refusing to call Destroy: the marker at %s reads %q, want %q; this Runtime is not the sandbox this test provisioned", markerPath, got, want)
	}

	status, err := rt.Status(ctx)
	if err != nil {
		t.Fatalf("Status before Destroy: %v", err)
	}
	if !status.Provisioned {
		t.Fatalf("refusing to call Destroy: Status reports Provisioned = false before this test provisioned anything")
	}

	if err := sess.Close(); err != nil {
		t.Fatalf("Close before Destroy: %v", err)
	}

	if err := rt.Destroy(ctx); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	after, err := rt.Status(ctx)
	if err != nil {
		t.Fatalf("Status after Destroy: %v", err)
	}
	if after.Provisioned {
		t.Errorf("Status after Destroy: Provisioned = true, want false")
	}
	if after.Running {
		t.Errorf("Status after Destroy: Running = true, want false")
	}

	if _, err := rt.StartSession(ctx, runtime.SessionSpec{User: contractUser, WorkDir: contractHome}); err == nil {
		t.Errorf("StartSession after Destroy: want an error wrapping ErrSandboxMissing, got nil")
	} else if !errors.Is(err, runtime.ErrSandboxMissing) {
		t.Errorf("StartSession after Destroy: err = %v, want errors.Is(err, ErrSandboxMissing)", err)
	}

	if err := rt.Destroy(ctx); err != nil {
		t.Errorf("second Destroy: %v, want nil (Destroy is documented as idempotent)", err)
	}
}

// TestCapabilitiesAreSelfConsistent asserts only what non-negotiable 2 and
// the interface doc comment actually promise: Privileged is always false,
// two consecutive calls return an identical value, the value does not
// change across Provision and Destroy, and the call does not block. It
// invents no relationship between capability bits.
func TestCapabilitiesAreSelfConsistent(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	ctx := contractContext(t)

	snapshot := func(when string) runtime.Caps {
		t.Helper()
		done := make(chan runtime.Caps, 1)
		go func() { done <- rt.Capabilities() }()
		select {
		case caps := <-done:
			if caps.Privileged {
				t.Errorf("Capabilities() %s: Privileged = true, want false (non-negotiable: never --privileged)", when)
			}
			return caps
		case <-time.After(2 * time.Second):
			t.Fatalf("Capabilities() %s did not return within 2 seconds", when)
			return runtime.Caps{}
		}
	}

	before := snapshot("before Provision")
	beforeAgain := snapshot("before Provision, second call")
	if before != beforeAgain {
		t.Errorf("Capabilities() returned different values on two consecutive calls: %+v vs %+v", before, beforeAgain)
	}

	if err := rt.Provision(ctx, ImageSpec()); err != nil {
		t.Fatalf("Provision(%q): %v", SandboxName, err)
	}
	afterProvision := snapshot("after Provision")
	if afterProvision != before {
		t.Errorf("Capabilities() changed after Provision: %+v vs %+v", before, afterProvision)
	}

	if err := rt.Destroy(ctx); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	afterDestroy := snapshot("after Destroy")
	if afterDestroy != before {
		t.Errorf("Capabilities() changed after Destroy: %+v vs %+v", before, afterDestroy)
	}
}

// TestNoNetworkByDefault leaves SessionSpec.Networking at its zero value,
// which is the default the product ships, and asserts that an outbound
// connection attempt fails fast rather than succeeding or hanging. A
// TimedOut result is treated as a failure of the assertion itself: per
// destructive-safety, this suite fails closed rather than reporting an
// inconclusive pass.
func TestNoNetworkByDefault(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	ctx := contractContext(t)

	if err := rt.Provision(ctx, ImageSpec()); err != nil {
		t.Fatalf("Provision(%q): %v", SandboxName, err)
	}
	// SessionSpec.Networking is deliberately left at its zero value, false.
	sess, err := rt.StartSession(ctx, runtime.SessionSpec{
		User:     contractUser,
		WorkDir:  contractHome,
		StateDir: contractStateDir,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	res, err := sess.Exec(ctx, []string{"/bin/bash", "-c", "exec 3<>/dev/tcp/1.1.1.1/443"}, runtime.ExecOpts{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Exec of the network probe: %v", err)
	}
	if res.TimedOut {
		t.Fatal("network probe timed out instead of failing fast; the connection attempt did not fail closed and the sandbox may have a network")
	}
	if res.ExitCode == 0 {
		t.Error("network probe succeeded with Networking left at its default (false); an outbound connection should fail")
	}
}

type assertion struct {
	name string
	run  func(*testing.T, Factory)
}

// contract is the whole suite, in the order the ticket lists it. Every
// exported assertion appears exactly once; shape_test.go enforces that.
var contract = []assertion{
	{"ProvisionIsIdempotent", TestProvisionIsIdempotent},
	{"StatusReportsProvisioned", TestStatusReportsProvisioned},
	{"ExecCapturesStreamsSeparately", TestExecCapturesStreamsSeparately},
	{"ExecReportsExitCode", TestExecReportsExitCode},
	{"ExecHonoursContextCancellation", TestExecHonoursContextCancellation},
	{"ExecRunsAsRequestedUser", TestExecRunsAsRequestedUser},
	{"PushFilesMaterializesContentModeOwner", TestPushFilesMaterializesContentModeOwner},
	{"PushFilesStripsCarriageReturns", TestPushFilesStripsCarriageReturns},
	{"PullFileRoundTrips", TestPullFileRoundTrips},
	{"DestroyThenStatusReportsMissing", TestDestroyThenStatusReportsMissing},
	{"CapabilitiesAreSelfConsistent", TestCapabilitiesAreSelfConsistent},
	{"NoNetworkByDefault", TestNoNetworkByDefault},
}

// RunContract runs every contract assertion against the Runtime that f
// builds, each as a named subtest with its own Runtime and its own cleanup.
//
// Call it from the implementation's own test package:
//
//	func TestDockerContract(t *testing.T) {
//	    runtimetest.RunContract(t, newDockerForTest)
//	}
//
// Assertions run in sequence, not in parallel: they provision and destroy
// one named sandbox and would collide.
func RunContract(t *testing.T, f Factory) {
	t.Helper()
	if f == nil {
		t.Fatal("runtimetest: RunContract needs a non-nil Factory")
	}
	for _, a := range contract {
		a := a
		t.Run(a.name, func(t *testing.T) { a.run(t, f) })
	}
}
