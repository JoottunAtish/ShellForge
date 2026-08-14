// Package ux renders user-facing output and errors.
//
// Layer L0. This package must not import any other internal package.
//
// The rule this package exists to enforce is non-negotiable number 6 in
// CLAUDE.md: every user-facing error says what failed, why, and the next command
// to run. A bare Go error must never reach the terminal.
package ux

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// docBaseURL is where a DocAnchor resolves to. Anchors are validated in CI
// against the headings that actually exist in the troubleshooting guide.
const docBaseURL = "https://github.com/JoottunAtish/ShellForge/blob/main/docs/05-troubleshooting.md"

// Error is a failure that is safe to show a learner.
//
// Construct one with Fail. Every field except Err is aimed at someone who does
// not know what a shell is and is worried they have broken their computer.
type Error struct {
	// Op is what the program was trying to do, in plain language.
	// Example: "provision the Windows sandbox".
	Op string

	// Err is the underlying cause, kept for %w unwrapping and for logs.
	Err error

	// Remediation is the next command to run, or the next thing to do.
	// This field is required. An Error without it is a bug.
	Remediation string

	// DocAnchor is a heading id in docs/05-troubleshooting.md, without the '#'.
	// Example: "virtualization-disabled". CI asserts the heading exists.
	DocAnchor string
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Err == nil {
		return e.Op
	}
	return e.Op + ": " + e.Err.Error()
}

// Unwrap exposes the underlying cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Err }

// Fail wraps err into a user-facing Error.
//
// op describes what was being attempted, lowercase and without trailing
// punctuation, in the same style as a wrapped error message. remediation is the
// next command the learner should run. docAnchor is a heading id in
// docs/05-troubleshooting.md, or the empty string when no page applies.
func Fail(op string, err error, remediation, docAnchor string) *Error {
	return &Error{Op: op, Err: err, Remediation: remediation, DocAnchor: docAnchor}
}

// Render writes err to w in the form a learner should see.
//
// A *Error is rendered with its remediation and documentation link. Any other
// error is rendered as an unexpected failure, which is itself a signal that the
// call site is missing a Fail wrapper.
func Render(w io.Writer, err error) {
	if err == nil {
		return
	}

	c := paletteFor(w)

	var ue *Error
	if !errors.As(err, &ue) {
		fmt.Fprintf(w, "%s %s\n", c.bad("Error:"), err.Error())
		fmt.Fprintf(w, "\nThis one is unexpected, which means it is our bug rather than yours.\n")
		fmt.Fprintf(w, "Please report it: run %s and attach the output.\n", c.cmd("shellforge bug-report"))
		return
	}

	fmt.Fprintf(w, "%s %s\n", c.bad("Error:"), ue.Op)
	if ue.Err != nil {
		fmt.Fprintf(w, "  %s\n", ue.Err.Error())
	}
	if ue.Remediation != "" {
		fmt.Fprintf(w, "\n%s %s\n", c.good("Try this:"), ue.Remediation)
	}
	if ue.DocAnchor != "" {
		fmt.Fprintf(w, "%s %s#%s\n", c.dim("More detail:"), docBaseURL, ue.DocAnchor)
	}
}

// palette holds the styling functions for one output stream.
type palette struct {
	bad  func(string) string
	good func(string) string
	dim  func(string) string
	cmd  func(string) string
}

func plain(s string) string { return s }

// paletteFor returns a colored palette when w is a terminal that wants color,
// and a plain one otherwise.
//
// Color is suppressed when NO_COLOR is set to anything, when TERM is "dumb", or
// when the stream is not a character device. See https://no-color.org.
func paletteFor(w io.Writer) palette {
	if !ColorEnabled(w) {
		return palette{bad: plain, good: plain, dim: plain, cmd: plain}
	}
	wrap := func(code string) func(string) string {
		return func(s string) string { return "\x1b[" + code + "m" + s + "\x1b[0m" }
	}
	return palette{
		bad:  wrap("1;31"),
		good: wrap("1;32"),
		dim:  wrap("2"),
		cmd:  wrap("1;36"),
	}
}

// ColorEnabled reports whether w should receive ANSI colour.
//
// It is exported so that a caller rendering something other than an Error
// asks the same question this package already answers for its own palette,
// rather than reimplementing the rule. There is exactly one place in the
// codebase that decides whether colour is wanted, and this is it.
//
// The answer is a property of the stream and the environment, not of the
// moment, so a caller that writes many times is expected to ask once and
// carry the answer. `check` replies are the case that matters: the reply
// travels through the sandbox and back, so there is no *os.File on the host
// to ask by the time it is rendered, and asking per reply would also mean a
// Stat syscall per keystroke of the learner's patience.
func ColorEnabled(w io.Writer) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
