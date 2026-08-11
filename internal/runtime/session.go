package runtime

import (
	"context"
	"io"
	"io/fs"
	"time"
)

// Session is one open connection to a provisioned sandbox.
//
// The same Session type serves the learner's interactive shell and the
// non-interactive probes that verification runs. The caller closes it.
//
// A Session is safe for concurrent use by multiple goroutines: Exec,
// Attach, PushFiles, and PullFile may be called concurrently on the same
// Session, and a call in progress on one goroutine must not corrupt or
// block a call in progress on another. Both planned backends run each Exec
// as its own subprocess, so this costs neither of them anything, and
// verification needs it: a check observes a process running inside the
// sandbox by calling PullFile or Exec on the same Session that started it,
// while that first call is still in flight.
//
// Close is the exception. A caller must not call Close concurrently with
// another in-flight call on the same Session; the outcome of that other
// call is unspecified if it does. Close itself is safe to call more than
// once.
type Session interface {
	// Exec runs argv inside the sandbox without a shell and waits for it to
	// finish.
	//
	// argv is a vector, never a command string, and it never reaches a
	// host shell. That is a security boundary rather than a style
	// preference: a string overload is how command injection arrives, so
	// the interface makes the unsafe call impossible to express. A check
	// that needs shell syntax passes its script as a single argument to
	// bash inside the sandbox.
	//
	// A non-zero exit status is reported in ExecResult.ExitCode, not as an
	// error. The error return means argv could not be run at all. A context
	// cancelled before or during the call returns context.Canceled. Whether a
	// binary missing from the sandbox surfaces as that error or as a
	// backend-defined non-zero ExitCode is deliberately not pinned by this
	// contract, so a check must not depend on a particular value for it.
	Exec(ctx context.Context, argv []string, opts ExecOpts) (ExecResult, error)

	// Attach starts an interactive process on a pseudo terminal and
	// returns the handle the PTY multiplexer drives. The caller closes the
	// PTY.
	Attach(ctx context.Context, opts AttachOpts) (PTY, error)

	// PushFiles materializes every entry in m inside the sandbox, creating
	// parent directories as needed. An implementation strips carriage
	// returns from the content, because a CRLF in a .sh file produces a
	// bad interpreter error that no beginner can diagnose.
	PushFiles(ctx context.Context, m FileManifest) error

	// PullFile reads an absolute path inside the sandbox and returns its
	// content. It does not modify the sandbox.
	PullFile(ctx context.Context, path string) ([]byte, error)

	// Close releases the session and everything it started. It is safe to
	// call more than once.
	Close() error
}

// PTY is the handle the PTY multiplexer drives.
//
// It is declared here rather than in internal/pty so that L1 does not have
// to import L2, which would invert the layer rule. internal/pty consumes
// this interface; it does not define it.
type PTY interface {
	io.ReadWriteCloser

	// Resize sets the terminal window size. The multiplexer calls it on
	// every SIGWINCH, because a missed resize corrupts a full screen
	// application.
	Resize(rows, cols uint16) error

	// Wait blocks until the attached process exits.
	Wait() error
}

// ExecOpts tunes a single Exec call.
//
// The zero value runs argv as the session user, in the session working
// directory, with no extra environment and no timeout of its own.
type ExecOpts struct {
	// User is the sandbox user to run as. Empty means the session user.
	// The caller must allowlist-validate it against
	// ^[a-zA-Z0-9][a-zA-Z0-9_-]*$ before it reaches an argv.
	User string

	// WorkDir is an absolute path inside the sandbox. Empty means the
	// session working directory.
	//
	// Like FileEntry.Path, it must resolve under /home/learner/. A value
	// that does not is refused rather than adjusted.
	WorkDir string

	// Env is added to the session environment for this call only.
	Env map[string]string

	// Stdin is written to the process standard input, which is then
	// closed.
	Stdin []byte

	// Timeout bounds this call. Zero means the context alone bounds it. A
	// call that exceeds it returns with TimedOut set rather than an error,
	// so a level can report a timeout as its own kind of failure. When
	// that happens, ExecResult.ExitCode does not carry a real process exit
	// status; see ExecResult.TimedOut.
	Timeout time.Duration
}

// ExecResult is what one Exec call produced.
//
// The streams stay separate on purpose. A combined output field would make
// it impossible for a script check to give a precise diagnostic, because it
// could no longer tell what the command printed from what it complained
// about.
type ExecResult struct {
	// Stdout is the standard output of the process.
	Stdout []byte

	// Stderr is the standard error of the process.
	Stderr []byte

	// ExitCode is the exit status. A non-zero value is a normal result,
	// not an error. When TimedOut is true, ExitCode is not a process exit
	// status: the process was killed before it could exit on its own, and
	// an implementation sets ExitCode to -1 so a caller cannot mistake a
	// killed process for one that exited cleanly.
	ExitCode int

	// Duration is how long the call took.
	Duration time.Duration

	// TimedOut reports that ExecOpts.Timeout elapsed and the process was
	// killed. When this is true, read ExitCode as -1, not as a pass or
	// fail exit status.
	TimedOut bool
}

// AttachOpts describes the interactive process to start on a pseudo
// terminal.
type AttachOpts struct {
	// User is the sandbox user to run as. Empty means the session user.
	// The caller must allowlist-validate it against
	// ^[a-zA-Z0-9][a-zA-Z0-9_-]*$ before it reaches an argv.
	User string

	// WorkDir is an absolute path inside the sandbox. Empty means the
	// session working directory.
	//
	// Like FileEntry.Path, it must resolve under /home/learner/. A value
	// that does not is refused rather than adjusted.
	WorkDir string

	// Env is added to the session environment for the attached process.
	Env map[string]string

	// Command is the argv to run. Empty means the instrumented bash
	// invocation, which is what a level normally wants.
	Command []string
}

// FileManifest is a set of files to materialize into the sandbox in one
// call.
type FileManifest struct {
	// Files are applied in order.
	Files []FileEntry
}

// FileEntry is one file to write inside the sandbox.
type FileEntry struct {
	// Path is absolute and inside the sandbox. It must resolve under the
	// level setup root, which lives under /home/learner/. This is what a
	// materialize call trusts and what a reset path later trusts again, so
	// it is checked twice: the caller validates it before calling
	// PushFiles, and an implementation checks it again before writing.
	// Both checks apply the same order: clean the path, check the prefix,
	// resolve symlinks, then check the prefix again, because a symlink
	// placed during the level can point outside the root even when the
	// unresolved path looks fine. A path that fails either check is
	// refused, never adjusted or truncated into place.
	Path string

	// Content is the file body. An implementation strips carriage returns
	// before writing it.
	Content []byte

	// Mode is the permission bits to set on the file.
	Mode fs.FileMode

	// Owner is an owner and group pair such as "learner:learner".
	Owner string
}
