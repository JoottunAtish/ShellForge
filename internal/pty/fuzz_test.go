package pty

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runParser feeds in through a Parser using the given clock, chunk bytes at a
// time (chunk <= 0 means the whole input in one Write), and returns the
// forwarded bytes and the events observed, including a trailing Flush.
func runParser(in []byte, chunk int, clock func() time.Time) ([]byte, []Event) {
	var out bytes.Buffer
	var events []Event
	p := NewParser(&out, func(e Event) { events = append(events, e) })
	p.now = clock

	if chunk <= 0 {
		chunk = len(in)
		if chunk == 0 {
			chunk = 1
		}
	}
	for i := 0; i < len(in); i += chunk {
		end := i + chunk
		if end > len(in) {
			end = len(in)
		}
		_, _ = p.Write(in[i:end])
	}
	_ = p.Flush()
	return out.Bytes(), events
}

// eventsEqual compares two event slices on the fields that matter to a
// consumer: Kind, ExitCode, and Cwd. At is deliberately excluded because the
// two runs in FuzzParser use a pinned clock anyway, and comparing it would
// only be re-testing the clock, not the parser.
func eventsEqual(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || a[i].ExitCode != b[i].ExitCode || a[i].Cwd != b[i].Cwd {
			return false
		}
	}
	return true
}

// isSubsequence reports whether out appears as a subsequence of in: every
// byte of out occurs in in, in order, though not necessarily contiguously.
// This is the invariant that the parser never invents or reorders bytes: a
// two-pointer walk, one pointer per slice, each only ever advancing forward.
func isSubsequence(out, in []byte) bool {
	j := 0
	for i := 0; i < len(out); i++ {
		for j < len(in) && in[j] != out[i] {
			j++
		}
		if j >= len(in) {
			return false
		}
		j++
	}
	return true
}

// FuzzParser is AC9: the parser must never panic on adversarial input, must
// produce identical output and events whether fed whole or one byte at a
// time, must only ever forward a subsequence of its input, and must never
// emit an event outside its documented shape.
func FuzzParser(f *testing.F) {
	for _, c := range recognizedCases() {
		f.Add([]byte(c.in))
	}
	for _, c := range hostileCases() {
		f.Add([]byte(c.in))
	}
	f.Add([]byte("\x1b]133;A\x07learner@quest:~$ \x1b]133;B\x07ls -la\r\n" +
		"\x1b]133;C\x07total 4\r\ndrwxr-xr-x 2 learner learner 4096 Aug 10 12:00 .\r\n" +
		"\x1b]133;D;0\x07\x1b]7;file://quest/home/learner\x07" +
		"\x1b]0;a title\x07" +
		"\x1b]133;A\x07learner@quest:~$ \x1b]133;B\x07false\r\n" +
		"\x1b]133;C\x07\x1b]133;D;1\x07\x1b]7;file://quest/home/learner/work%20dir\x07"))
	f.Add([]byte("\x1b]" + strings.Repeat("x", MaxOSCPayload+10) + "\x07"))
	for _, in := range []string{
		"\x1b",
		"\x1b]",
		"\x1b]133;",
		"\x1b]133;D;0",
		"\x1b]133;A\x1b",
		"hello\x1b",
	} {
		f.Add([]byte(in))
	}
	if vim, err := os.ReadFile(filepath.Join("testdata", "vim-session.bin")); err == nil {
		f.Add(vim)
	}

	fixed := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixed }

	f.Fuzz(func(t *testing.T, in []byte) {
		whole, wholeEvents := runParser(in, 0, clock)
		byByte, byByteEvents := runParser(in, 1, clock)

		if !bytes.Equal(whole, byByte) {
			t.Fatalf("chunking changed forwarded bytes: whole %q, one-byte %q", whole, byByte)
		}
		if !eventsEqual(wholeEvents, byByteEvents) {
			t.Fatalf("chunking changed events: whole %+v, one-byte %+v", wholeEvents, byByteEvents)
		}
		if !isSubsequence(whole, in) {
			t.Fatalf("forwarded bytes %q are not a subsequence of input %q", whole, in)
		}
		// A stream with no ESC in it contains no sequence to strip, so the
		// parser must be the identity function on it. isSubsequence alone
		// would accept a parser that silently dropped bytes here.
		if !bytes.Contains(in, []byte{0x1b}) {
			if !bytes.Equal(whole, in) {
				t.Fatalf("no ESC in input but output %q differs from input %q", whole, in)
			}
			if len(wholeEvents) != 0 {
				t.Fatalf("no ESC in input but got %d events: %+v", len(wholeEvents), wholeEvents)
			}
		}
		// Stripping only ever happens for a recognized marker, so no events
		// means nothing was stripped and the passthrough must be byte
		// identical.
		if len(wholeEvents) == 0 && !bytes.Equal(whole, in) {
			t.Fatalf("0 events but output %q differs from input %q", whole, in)
		}
		for _, e := range wholeEvents {
			switch e.Kind {
			case PromptStart, CommandStart, PreExec:
				if e.ExitCode != 0 || e.Cwd != "" {
					t.Fatalf("event %s carries unexpected payload: %+v", e.Kind, e)
				}
			case CommandDone:
				if e.ExitCode < 0 || e.ExitCode > 255 {
					t.Fatalf("CommandDone with ExitCode outside 0-255: %+v", e)
				}
			case CwdReport:
				if !strings.HasPrefix(e.Cwd, "/") {
					t.Fatalf("CwdReport without a leading slash: %+v", e)
				}
			default:
				t.Fatalf("event with unrecognized Kind: %+v", e)
			}
		}

		var out bytes.Buffer
		p := NewParser(&out, nil)
		p.now = clock
		_, _ = p.Write(in)
		_ = p.Flush()
		beforeSecondFlush := out.Len()
		if err := p.Flush(); err != nil {
			t.Fatalf("second Flush returned an error: %v", err)
		}
		if out.Len() != beforeSecondFlush {
			t.Fatalf("second Flush forwarded %d additional bytes, want 0", out.Len()-beforeSecondFlush)
		}
	})
}
