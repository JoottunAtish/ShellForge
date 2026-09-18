// Command sf-ptyhost allocates a pseudo terminal inside the sandbox and
// runs the learner's shell in it, shuttling raw bytes over its own stdin
// and stdout.
//
// It exists because the pseudo terminal was on the wrong side of the host
// and sandbox boundary. Both backends used to allocate one on the HOST,
// around `docker exec -it` or `wsl.exe --exec`, with creack/pty. That
// package returns ErrUnsupported unconditionally on every Windows build,
// so a learner on a native Windows console could not open a shell at all,
// which is issue #138.
//
// Putting the pseudo terminal where the shell already is removes the
// problem rather than working around it. The host then needs only two
// ordinary pipes, which every operating system has, plus the console mode
// handling internal/platform already does. This is how an SSH client works
// on Windows: the console interprets the VT stream directly and the pty
// lives on the far side, where the shell is.
//
// The alternative was a host side ConPTY. It was rejected because ConPTY
// is a second, full VT emulator in the path with no passthrough mode: it
// front-loads OSC sequences and does not preserve their order relative to
// surrounding text. images/rc/instrument.bash emits OSC 133 markers and
// internal/pty/osc.go parses them out of the byte stream to build the
// journal, and the journal is what verification, scoring and par are
// computed from. A host ConPTY would have corrupted it silently, which is
// worse than the refusal it replaced.
//
// Usage:
//
//	sf-ptyhost --winsize-fifo <path> -- <command> [args...]
//
// It is built into the sandbox image, not shipped to the host. See
// images/Containerfile.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

func main() {
	fifo := flag.String("winsize-fifo", "", "path to the FIFO the host writes \"rows cols\" lines to")
	flag.Parse()

	command := flag.Args()
	if len(command) == 0 {
		fmt.Fprintln(os.Stderr, "sf-ptyhost: no command given")
		fmt.Fprintln(os.Stderr, "usage: sf-ptyhost --winsize-fifo <path> -- <command> [args...]")
		os.Exit(2)
	}

	code, err := run(*fifo, command)
	if err != nil {
		// stderr, never stdout. stdout carries the pseudo terminal's
		// bytes verbatim and nothing else: the host parses OSC 133 out
		// of that stream, so a diagnostic written there would arrive
		// inside the learner's shell output and could be read as a
		// marker.
		fmt.Fprintf(os.Stderr, "sf-ptyhost: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// errUnsupported is what run returns on a platform with no pseudo terminal
// support. Nothing ships this binary anywhere but inside the Linux sandbox;
// the stub exists so that `GOOS=windows go build ./...`, which CI runs on
// every push, still covers this package rather than skipping it.
var errUnsupported = errors.New("sf-ptyhost runs inside the Linux sandbox and has no implementation for this platform")
