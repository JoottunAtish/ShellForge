package runtimetest

// THIS SUITE DESTROYS SANDBOXES. READ THIS BEFORE WRITING A FACTORY.
//
// Several assertions call Runtime.Destroy. On the WSL backend, Destroy
// eventually means wsl --unregister, which is instant, irreversible, and
// has no undo. A Factory that hands this suite a Runtime pointed at the
// developer's real sandbox, or at any distribution the developer cares
// about, is a bug, and the consequence is somebody's Linux installation
// gone for good.
//
// A Factory must therefore build a Runtime scoped to SandboxName, which
// is a fixed test-only constant. Never the production sandbox name, never
// a name read from the environment, never a name derived from anything a
// test can influence. This is the ONLY guard this suite has. Read the
// next two paragraphs before assuming otherwise.
//
// The suite controls what it provisions and cannot control what Destroy
// removes. Runtime.Destroy takes no name, and ImageSpec.Name documents
// that a destroy path must derive its target from a package constant of
// its own, never from the spec. So a Factory that returns a backend's
// ordinary production constructor, instead of a test-only constructor
// whose compile-time destroy target happens to be SandboxName, provisions
// shellforge-contracttest and destroys the production sandbox instead.
// Every downstream assertion still goes green: Status carries no sandbox
// identity (tracked as F2 in issue #32), so nothing in this package can
// detect that the wrong sandbox was removed. Exposing a test-only
// constructor whose destroy constant is SandboxName is the backend
// author's responsibility; returning the production constructor from a
// Factory is the specific mistake that loses a distribution.
//
// An earlier version of this suite wrote a marker file before Destroy and
// read it back, calling that an identity check. It was not one: pushing a
// file and reading the same file back through the same Session proves
// PushFiles and PullFile round trip, which TestPullFileRoundTrips already
// covers, and nothing about which sandbox is on the other end. That
// mechanism has been removed. What each assertion that provisions does
// instead is call ensureNoSandbox first: Destroy, then assert Status
// reports Provisioned false, then Provision. That proves the assertion's
// own precondition rather than assuming it, and it means any Destroy call
// later in that same assertion is acting on a sandbox the assertion is
// provably the one that created. It proves nothing about a sandbox this
// suite did not create; if the Factory is wrong, the sandbox is already
// lost the moment Destroy is first called, exactly as before.
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

// ensureNoSandbox establishes a known-clean precondition: it calls Destroy,
// which is documented idempotent, and then asserts Status reports
// Provisioned false. Every assertion that provisions calls this first,
// rather than assuming the Runtime a Factory handed it starts clean.
//
// This is what gives a later Destroy call in the same assertion its
// authority: the assertion observed the sandbox absent and then created it
// itself, so the thing it goes on to destroy is one it made. See the file
// header for what this does and does not prove.
//
// Calling Destroy here adds a call site beyond the ones that already
// existed. That is deliberate: the suite's authority to destroy anything at
// all comes entirely from the Factory building a Runtime scoped to
// SandboxName, and if the Factory is wrong the sandbox is already lost at
// the pre-existing call sites regardless. One more call does not change the
// risk class; it buys a precondition that is checked instead of assumed.
func ensureNoSandbox(ctx context.Context, t *testing.T, rt runtime.Runtime) {
	t.Helper()
	if err := rt.Destroy(ctx); err != nil {
		t.Fatalf("Destroy establishing the no-sandbox precondition: %v, want nil (Destroy is documented as idempotent)", err)
	}
	status, err := rt.Status(ctx)
	if err != nil {
		t.Fatalf("Status establishing the no-sandbox precondition: %v", err)
	}
	if status.Provisioned {
		t.Fatalf("Status after Destroy: Provisioned = true, want false; this Runtime cannot establish a known-clean precondition, so nothing later in this assertion can trust what it destroys")
	}
}

// provisionedSession establishes the no-sandbox precondition, provisions the
// contract sandbox, and opens a session on it with networking off, which is
// the default the product ships.
func provisionedSession(ctx context.Context, t *testing.T, rt runtime.Runtime) runtime.Session {
	t.Helper()
	ensureNoSandbox(ctx, t, rt)
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
	ensureNoSandbox(ctx, t, rt)

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
	ensureNoSandbox(ctx, t, rt)

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

// TestExecWritesStdin asserts that ExecOpts.Stdin actually reaches the
// process's standard input and is then closed.
//
// This assertion exists because its absence hid a real defect. The docker
// backend set standard input on the local `docker` process but never passed
// -i, so `docker exec` connected the container process's standard input to
// nothing: every byte of ExecOpts.Stdin was silently discarded and the process
// saw an immediate EOF. Nothing in this suite covered the field, so the whole
// suite passed while a documented part of the interface did not work. It
// surfaced only when the demo level's control channel replied with zero bytes
// and `check` printed nothing.
//
// Both halves matter. A backend that delivers the bytes but never closes
// standard input passes the first check and hangs forever on the second,
// because `cat` with no redirect reads until EOF.
func TestExecWritesStdin(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	ctx := contractContext(t)
	sess := provisionedSession(ctx, t, rt)

	const payload = "shellforge-stdin-payload\n"

	// cat with no argument reads standard input until EOF, so this asserts
	// delivery and closure at once: an unclosed stdin never terminates and
	// the assertion fails by timeout rather than by comparison.
	res, err := sess.Exec(ctx, []string{"cat"}, runtime.ExecOpts{Stdin: []byte(payload)})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.TimedOut {
		t.Fatalf("Exec: TimedOut = true, want false. Standard input was probably never closed, so cat read forever.")
	}
	if res.ExitCode != 0 {
		t.Fatalf("Exec exit code: want 0, got %d (stderr: %q)", res.ExitCode, res.Stderr)
	}
	if !bytes.Contains(res.Stdout, []byte("shellforge-stdin-payload")) {
		t.Errorf("ExecOpts.Stdin never reached the process: cat echoed %q, want it to contain the payload.\n"+
			"On a docker backend this is what a missing -i on `docker exec` looks like.", res.Stdout)
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

		// Clear any pid file a previous run of this assertion left behind.
		// Without this, the poll below can observe a stale pid immediately,
		// before the process this run starts has written anything, and the
		// orphan probe at the end would then check a pid that was never
		// running in this run at all: it would pass without proving
		// anything.
		if _, err := sess.Exec(base, []string{"/bin/sh", "-c", "rm -f " + pidPath}, runtime.ExecOpts{}); err != nil {
			t.Fatalf("clearing a stale pid file before the run: %v", err)
		}

		execCtx, cancel := context.WithCancel(base)
		t.Cleanup(cancel) // idempotent with the explicit cancel() below; guards an early t.Fatal.

		errCh := make(chan error, 1)
		go func() {
			_, err := sess.Exec(execCtx, []string{"/bin/sh", "-c", "echo $$ > " + pidPath + "; exec sleep 30"}, runtime.ExecOpts{})
			errCh <- err
		}()

		deadline := time.Now().Add(2 * time.Second)
		var pid string
		for time.Now().Before(deadline) {
			got, err := sess.PullFile(base, pidPath)
			if err == nil {
				if p := strings.TrimSpace(string(got)); p != "" {
					pid = p
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		if pid == "" {
			t.Fatal("the sandbox-side process never wrote its pid within 2 seconds")
		}
		// pid came from inside the sandbox and is about to be interpolated
		// into a command argument. Refuse it if it is not exactly what
		// "echo $$" can produce, rather than trusting it.
		for _, r := range pid {
			if r < '0' || r > '9' {
				t.Fatalf("pid file content is not all ASCII digits, refusing to use it: %q", pid)
			}
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

		// Use the pid this run captured rather than re-reading pidPath: a
		// re-read cannot tell a value this run wrote from a value a prior
		// run left behind.
		probe, err := sess.Exec(base, []string{"/bin/sh", "-c", "test -d /proc/" + pid}, runtime.ExecOpts{})
		if err != nil {
			t.Fatalf("orphan probe Exec: %v", err)
		}
		if probe.ExitCode == 0 {
			t.Errorf("orphan probe: /proc/%s still exists after cancellation, the sandbox-side process was not reaped", pid)
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

// sanitizeName turns a table case name into a path component inside the
// sandbox. It refuses, rather than rewrites, any character outside the
// small set it knows how to turn into a safe path component: the house rule
// for anything that becomes a path is refuse an unexpected input, never
// adjust it into place, and FileEntry.Path says so explicitly. Every case
// name here is a compile-time literal today, so nothing can escape yet, but
// a future case name containing "/" or ".." must fail loudly instead of
// silently producing a path outside contractRoot.
//
// A hyphen is accepted because this function already emits one: it is what
// a space is rewritten to just below. Refusing it on input while producing
// it on output made the "utf-8 multibyte" case in TestPullFileRoundTrips
// fail on its own name, for every backend, before that case could assert
// anything about the backend at all.
func sanitizeName(t *testing.T, name string) string {
	t.Helper()
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == ' ' || r == ',' || r == '\'' || r == '-':
		default:
			t.Fatalf("sanitizeName: table case name %q contains unexpected character %q; refusing to turn it into a path rather than adjusting it", name, r)
		}
	}
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
			path := contractRoot + "/roundtrip-" + sanitizeName(t, tc.name) + ".txt"
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

// TestDestroyThenStatusReportsMissing is the safety-critical assertion.
// provisionedSession calls ensureNoSandbox before it provisions, so by the
// time this test calls Destroy it has already proven the sandbox it is
// about to remove is one it created itself, not a preexisting one a
// misconfigured Factory happened to hand it. See the file-level comment for
// exactly what that does and does not guarantee.
func TestDestroyThenStatusReportsMissing(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	ctx := contractContext(t)
	sess := provisionedSession(ctx, t, rt)

	status, err := rt.Status(ctx)
	if err != nil {
		t.Fatalf("Status before Destroy: %v", err)
	}
	if !status.Provisioned {
		t.Fatalf("Status before Destroy: Provisioned = false, want true; this test provisioned the sandbox itself")
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
	ensureNoSandbox(ctx, t, rt)

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
// connection attempt fails fast rather than succeeding or hanging.
//
// A non-zero exit code on its own is not accepted as proof, because the
// probe fails the same way whether the network is blocked or the probe
// itself cannot run. All three ways it can be unusable fail the assertion
// rather than pass it: a TimedOut result, a bash that will not run a
// trivial command, and a bash without the /dev/tcp redirection built in.
// Per destructive-safety, this suite fails closed rather than reporting an
// inconclusive pass.
func TestNoNetworkByDefault(t *testing.T, f Factory) {
	rt := newRuntime(t, f)
	ctx := contractContext(t)
	ensureNoSandbox(ctx, t, rt)

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

	// The probe below is a bash feature: no POSIX shell has /dev/tcp. An
	// image with no bash at all would fail the probe for a reason that has
	// nothing to do with the network, and a failure that is indistinguishable
	// from success of the isolation is the one outcome this assertion must
	// never report. So establish that bash runs at all before the probe's
	// verdict is worth anything.
	if check, err := sess.Exec(ctx, []string{"/bin/bash", "-c", "exit 0"}, runtime.ExecOpts{Timeout: 5 * time.Second}); err != nil {
		t.Fatalf("network probe mechanism unusable: running /bin/bash at all: %v; this assertion cannot verify network isolation here", err)
	} else if check.ExitCode != 0 {
		t.Fatalf("network probe mechanism unusable: /bin/bash exited %d for a trivial command (stderr: %q); this assertion cannot verify network isolation here", check.ExitCode, check.Stderr)
	}

	res, err := sess.Exec(ctx, []string{"/bin/bash", "-c", "exec 3<>/dev/tcp/1.1.1.1/443"}, runtime.ExecOpts{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Exec of the network probe: %v", err)
	}
	if res.TimedOut {
		t.Fatal("network probe timed out instead of failing fast; the connection attempt did not fail closed and the sandbox may have a network")
	}

	// A non-zero exit alone proves nothing about network isolation: bash
	// reports the same shape of failure whether the connection was actually
	// refused or the /dev/tcp redirection form is simply not built into
	// this bash (a busybox or Alpine-derived rootfs, for one, or a bash
	// built without --enable-net-redirections). When the feature itself is
	// missing, bash treats the /dev/tcp path as a literal, nonexistent
	// file, which is a different failure than a refused or unreachable
	// connection. Fail the assertion outright when the probe cannot tell
	// the difference, rather than reporting a pass that proves nothing.
	if strings.Contains(string(res.Stderr), "No such file or directory") {
		t.Fatalf("network probe mechanism unusable: bash does not appear to support the /dev/tcp redirection on this image (stderr: %q); this assertion cannot verify network isolation here", res.Stderr)
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
	{"ExecWritesStdin", TestExecWritesStdin},
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
