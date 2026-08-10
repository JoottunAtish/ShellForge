package pty

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recognizedCases is the single source of truth for every marker this parser
// recognizes. TestParserRecognizesMarkers, TestParserMarkerSplitAtEveryOffset,
// and FuzzParser's seed corpus all consume this table so there is exactly one
// place that names a literal marker byte sequence.
func recognizedCases() []struct {
	name string
	in   string
	want Event
} {
	return []struct {
		name string
		in   string
		want Event
	}{
		{"prompt start BEL", "\x1b]133;A\x07", Event{Kind: PromptStart}},
		{"prompt start ST", "\x1b]133;A\x1b\\", Event{Kind: PromptStart}},
		{"command start BEL", "\x1b]133;B\x07", Event{Kind: CommandStart}},
		{"command start ST", "\x1b]133;B\x1b\\", Event{Kind: CommandStart}},
		{"preexec BEL", "\x1b]133;C\x07", Event{Kind: PreExec}},
		{"preexec ST", "\x1b]133;C\x1b\\", Event{Kind: PreExec}},
		{"done zero BEL", "\x1b]133;D;0\x07", Event{Kind: CommandDone, ExitCode: 0}},
		{"done 127 ST", "\x1b]133;D;127\x1b\\", Event{Kind: CommandDone, ExitCode: 127}},
		{"done 130 BEL", "\x1b]133;D;130\x07", Event{Kind: CommandDone, ExitCode: 130}},
		{"cwd home BEL", "\x1b]7;file://quest/home/learner\x07", Event{Kind: CwdReport, Cwd: "/home/learner"}},
		{"cwd root ST", "\x1b]7;file://quest/\x1b\\", Event{Kind: CwdReport, Cwd: "/"}},
		{"cwd percent space", "\x1b]7;file://quest/home/learner/my%20dir\x07", Event{Kind: CwdReport, Cwd: "/home/learner/my dir"}},
		{"cwd percent slash", "\x1b]7;file://quest/home/a%2Fb\x07", Event{Kind: CwdReport, Cwd: "/home/a/b"}},
		{"cwd empty host", "\x1b]7;file:///home/learner\x07", Event{Kind: CwdReport, Cwd: "/home/learner"}},
		{"cwd other host", "\x1b]7;file://sandbox/srv\x07", Event{Kind: CwdReport, Cwd: "/srv"}},
	}
}

// hostileCases is the table of adversarial payloads that must never corrupt
// parser state, never panic, and never emit a bogus event. Every row is
// forwarded byte for byte because nothing here is a fully recognized marker.
func hostileCases() []struct {
	name string
	in   string
} {
	return []struct {
		name string
		in   string
	}{
		{"forged fake subcommand", "\x1b]133;fake\x07"},
		{"unknown 133 subcommand", "\x1b]133;Z\x07"},
		{"133 A with parameters", "\x1b]133;A;aid=7\x07"},
		{"done non numeric", "\x1b]133;D;notanumber\x07"},
		{"done empty exit", "\x1b]133;D;\x07"},
		{"done bare no exit", "\x1b]133;D\x07"},
		{"done negative", "\x1b]133;D;-1\x07"},
		{"done leading plus", "\x1b]133;D;+1\x07"},
		{"done out of range", "\x1b]133;D;99999999999999999999\x07"},
		{"cwd no scheme", "\x1b]7;/home/learner\x07"},
		{"cwd wrong scheme", "\x1b]7;http://quest/home\x07"},
		{"cwd bad percent", "\x1b]7;file://quest/home/%zz\x07"},
		{"cwd truncated percent", "\x1b]7;file://quest/home/%2\x07"},
		{"cwd no path separator", "\x1b]7;file://quest\x07"},
		{"window title zero", "\x1b]0;my title\x07"},
		{"esc then non backslash in payload", "\x1b]133;A\x1bX\x07"},
		{"esc esc backslash in payload", "\x1b]0;a\x1b\x1b\\"},
		{"csi colour", "\x1b[1;31mred\x1b[0m"},
		{"esc esc", "\x1b\x1b"},
		{"utf8 multibyte", "h\xc3\xa9llo \xe4\xb8\x96\xe7\x95\x8c"},
		{"nul and high bytes", "\x00\xff\xfe\x80"},
		{"empty osc payload", "\x1b]\x07"},
		{"osc introducer then st only", "\x1b]\x1b\\"},
	}
}

// TestParserRecognizesMarkers is AC6 plus AC7: both terminators (BEL and ESC
// backslash), the exit code decoded as an int, and the cwd percent-decoded.
func TestParserRecognizesMarkers(t *testing.T) {
	for _, c := range recognizedCases() {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			var got []Event
			p := NewParser(&out, func(e Event) { got = append(got, e) })
			if _, err := p.Write([]byte(c.in)); err != nil {
				t.Fatalf("Write: unexpected error: %v", err)
			}
			if err := p.Flush(); err != nil {
				t.Fatalf("Flush: unexpected error: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("want exactly 1 event, got %d: %+v", len(got), got)
			}
			if got[0].Kind != c.want.Kind {
				t.Errorf("Kind: want %s, got %s", c.want.Kind, got[0].Kind)
			}
			if got[0].ExitCode != c.want.ExitCode {
				t.Errorf("ExitCode: want %d, got %d", c.want.ExitCode, got[0].ExitCode)
			}
			if got[0].Cwd != c.want.Cwd {
				t.Errorf("Cwd: want %q, got %q", c.want.Cwd, got[0].Cwd)
			}
			if out.Len() != 0 {
				t.Errorf("out: want 0 bytes leaked, got %d: %q", out.Len(), out.Bytes())
			}
		})
	}
}

// TestParserMarkerSplitAtEveryOffset is AC1: a marker split across two Write
// calls at every possible byte offset must still be recognized exactly once
// with nothing leaked. This is the test that catches a buffered scan that
// only works when a marker arrives whole.
func TestParserMarkerSplitAtEveryOffset(t *testing.T) {
	for _, c := range recognizedCases() {
		for i := 1; i < len(c.in); i++ {
			t.Run(fmt.Sprintf("%s/split-at-%d", c.name, i), func(t *testing.T) {
				var out bytes.Buffer
				var got []Event
				p := NewParser(&out, func(e Event) { got = append(got, e) })
				if _, err := p.Write([]byte(c.in[:i])); err != nil {
					t.Fatalf("first Write: unexpected error: %v", err)
				}
				if _, err := p.Write([]byte(c.in[i:])); err != nil {
					t.Fatalf("second Write: unexpected error: %v", err)
				}
				if err := p.Flush(); err != nil {
					t.Fatalf("Flush: unexpected error: %v", err)
				}
				if len(got) != 1 {
					t.Fatalf("split at %d: want exactly 1 event, got %d: %+v", i, len(got), got)
				}
				if got[0].Kind != c.want.Kind || got[0].ExitCode != c.want.ExitCode || got[0].Cwd != c.want.Cwd {
					t.Errorf("split at %d: want %+v, got %+v", i, c.want, got[0])
				}
				if out.Len() != 0 {
					t.Errorf("split at %d: out: want 0 bytes leaked, got %d: %q", i, out.Len(), out.Bytes())
				}
			})
		}
	}
}

// TestParserOneByteAtATime is AC2: the same realistic session fed whole, one
// byte per Write, and in several fixed chunk sizes must produce the identical
// event slice and the identical forwarded byte string every time.
func TestParserOneByteAtATime(t *testing.T) {
	const session = "\x1b]133;A\x07learner@quest:~$ \x1b]133;B\x07ls -la\r\n" +
		"\x1b]133;C\x07total 4\r\ndrwxr-xr-x 2 learner learner 4096 Aug 10 12:00 .\r\n" +
		"\x1b]133;D;0\x07\x1b]7;file://quest/home/learner\x07" +
		"\x1b]0;a title\x07" +
		"\x1b]133;A\x07learner@quest:~$ \x1b]133;B\x07false\r\n" +
		"\x1b]133;C\x07\x1b]133;D;1\x07\x1b]7;file://quest/home/learner/work%20dir\x07"

	wantEvents := []Event{
		{Kind: PromptStart},
		{Kind: CommandStart},
		{Kind: PreExec},
		{Kind: CommandDone, ExitCode: 0},
		{Kind: CwdReport, Cwd: "/home/learner"},
		{Kind: PromptStart},
		{Kind: CommandStart},
		{Kind: PreExec},
		{Kind: CommandDone, ExitCode: 1},
		{Kind: CwdReport, Cwd: "/home/learner/work dir"},
	}

	wantOut := stripRecognized(session)

	run := func(t *testing.T, chunk int) ([]Event, string) {
		var out bytes.Buffer
		var got []Event
		p := NewParser(&out, func(e Event) { got = append(got, e) })
		b := []byte(session)
		if chunk <= 0 {
			chunk = len(b)
		}
		for i := 0; i < len(b); i += chunk {
			end := i + chunk
			if end > len(b) {
				end = len(b)
			}
			if _, err := p.Write(b[i:end]); err != nil {
				t.Fatalf("Write: unexpected error: %v", err)
			}
		}
		if err := p.Flush(); err != nil {
			t.Fatalf("Flush: unexpected error: %v", err)
		}
		return got, out.String()
	}

	assertMatches := func(t *testing.T, got []Event, gotOut string) {
		if len(got) != len(wantEvents) {
			t.Fatalf("want %d events, got %d: %+v", len(wantEvents), len(got), got)
		}
		for i, w := range wantEvents {
			if got[i].Kind != w.Kind || got[i].ExitCode != w.ExitCode || got[i].Cwd != w.Cwd {
				t.Errorf("event %d: want %+v, got %+v", i, w, got[i])
			}
		}
		if gotOut != wantOut {
			t.Errorf("forwarded bytes mismatch:\nwant %q\ngot  %q", wantOut, gotOut)
		}
	}

	t.Run("whole", func(t *testing.T) {
		got, gotOut := run(t, 0)
		assertMatches(t, got, gotOut)
	})
	t.Run("one-byte", func(t *testing.T) {
		got, gotOut := run(t, 1)
		assertMatches(t, got, gotOut)
	})
	for _, chunk := range []int{1, 2, 3, 5, 7, 64, 4096} {
		t.Run(fmt.Sprintf("chunk-%d", chunk), func(t *testing.T) {
			got, gotOut := run(t, chunk)
			assertMatches(t, got, gotOut)
		})
	}
}

// stripRecognized computes the expected forwarded output for
// TestParserOneByteAtATime by hand: the five recognized markers vanish, the
// window title sequence survives, and everything else is untouched. This is
// intentionally independent of the parser implementation.
func stripRecognized(session string) string {
	replacements := []string{
		"\x1b]133;A\x07", "",
		"\x1b]133;B\x07", "",
		"\x1b]133;C\x07", "",
		"\x1b]133;D;0\x07", "",
		"\x1b]133;D;1\x07", "",
		"\x1b]7;file://quest/home/learner\x07", "",
		"\x1b]7;file://quest/home/learner/work%20dir\x07", "",
	}
	out := session
	for i := 0; i < len(replacements); i += 2 {
		out = strings.ReplaceAll(out, replacements[i], replacements[i+1])
	}
	return out
}

// testdata/vim-session.bin is raw PTY master output, 4601 bytes, captured once
// from /usr/bin/vim driven through a python pty.fork harness at 80x24 with
// TERM=xterm-256color and:
//
//	vim -u NONE -i NONE -n -c "set title titlestring=shellforge-fixture ttyfast nomore"
//
// There is no script(1) header and nothing is appended. It contains 184 CSI
// sequences, two OSC window title sequences, 19 CRLF pairs, 7 lone CRs, and no
// shellforge marker, so byte identity here is a strict equality with zero bytes
// added and zero removed. Regenerate it the same way if vim's output ever needs
// refreshing, and expect the counts in TestVimFixtureIsUnmodified to change.
func TestParserVimSessionByteIdentical(t *testing.T) {
	in, err := os.ReadFile(filepath.Join("testdata", "vim-session.bin"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	run := func(t *testing.T, chunk int) ([]Event, []byte) {
		var out bytes.Buffer
		var got []Event
		p := NewParser(&out, func(e Event) { got = append(got, e) })
		if chunk <= 0 {
			chunk = len(in)
		}
		for i := 0; i < len(in); i += chunk {
			end := i + chunk
			if end > len(in) {
				end = len(in)
			}
			if _, err := p.Write(in[i:end]); err != nil {
				t.Fatalf("Write: unexpected error: %v", err)
			}
		}
		if err := p.Flush(); err != nil {
			t.Fatalf("Flush: unexpected error: %v", err)
		}
		return got, out.Bytes()
	}

	check := func(t *testing.T, chunk int) {
		events, out := run(t, chunk)
		if len(events) != 0 {
			t.Fatalf("want 0 events from a marker-free vim session, got %d: %+v", len(events), events)
		}
		if !bytes.Equal(out, in) {
			reportFirstDiff(t, in, out)
		}
		if len(out) != len(in) {
			t.Fatalf("want output length %d, got %d", len(in), len(out))
		}
	}

	t.Run("whole", func(t *testing.T) { check(t, 0) })
	t.Run("one-byte", func(t *testing.T) { check(t, 1) })
	for _, chunk := range []int{1, 2, 3, 5, 7, 64, 4096} {
		t.Run(fmt.Sprintf("chunk-%d", chunk), func(t *testing.T) { check(t, chunk) })
	}
}

// reportFirstDiff prints the first differing offset and 16 bytes of context on
// each side. A byte-for-byte diff of 4601 bytes is unreadable without this.
func reportFirstDiff(t *testing.T, want, got []byte) {
	t.Helper()
	n := len(want)
	if len(got) < n {
		n = len(got)
	}
	for i := 0; i < n; i++ {
		if want[i] != got[i] {
			lo := i - 16
			if lo < 0 {
				lo = 0
			}
			hiWant := i + 16
			if hiWant > len(want) {
				hiWant = len(want)
			}
			hiGot := i + 16
			if hiGot > len(got) {
				hiGot = len(got)
			}
			t.Fatalf("first differing byte at offset %d\nwant: %q\ngot:  %q", i, want[lo:hiWant], got[lo:hiGot])
		}
	}
	t.Fatalf("outputs differ in length: want %d, got %d", len(want), len(got))
}

// TestVimFixtureIsUnmodified is the tripwire for the accident described in
// the ticket's docs and CI section: git currently leaves the CRs in
// vim-session.bin alone only because the seven lone CRs make text=auto guess
// binary. It passes from the first moment, before osc.go exists, because it
// only reads the file. It is not redundant with TestParserVimSessionByteIdentical:
// it protects the fixture itself, not the parser.
func TestVimFixtureIsUnmodified(t *testing.T) {
	const msg = "the vim fixture changed. If you did not re-capture it, a line ending rewrite did this. Check that .gitattributes marks *.bin binary and re-checkout the file."

	in, err := os.ReadFile(filepath.Join("testdata", "vim-session.bin"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if len(in) != 4601 {
		t.Errorf("%s (length: want 4601, got %d)", msg, len(in))
	}
	if n := bytes.Count(in, []byte("\r\n")); n != 19 {
		t.Errorf("%s (CRLF pairs: want 19, got %d)", msg, n)
	}
	if n := bytes.Count(in, []byte{0x1b, ']'}); n != 2 {
		t.Errorf("%s (OSC introducers: want 2, got %d)", msg, n)
	}
	if n := bytes.Count(in, []byte("133;")); n != 0 {
		t.Errorf("%s (shellforge 133 marker: want 0, got %d)", msg, n)
	}
	if n := bytes.Count(in, []byte("7;file://")); n != 0 {
		t.Errorf("%s (shellforge cwd marker: want 0, got %d)", msg, n)
	}
	if n := bytes.Count(in, []byte{0x1b, '\\'}); n != 0 {
		t.Errorf("%s (ST terminators: want 0, got %d)", msg, n)
	}
	for _, b := range in {
		if b > 126 {
			t.Errorf("%s (byte value %d exceeds 126)", msg, b)
			break
		}
	}
	if bytes.Contains(in, []byte{0x00}) {
		t.Errorf("%s (NUL byte present)", msg)
	}
}

// TestParserUnrecognizedOSCPassthrough is AC4: an unrecognized OSC sequence is
// forwarded byte for byte, and a genuine marker immediately afterward is
// still recognized, which proves the parser's state stays clean.
func TestParserUnrecognizedOSCPassthrough(t *testing.T) {
	parts := []string{
		"\x1b]0;window title\x07",
		"\x1b]133;A\x07",
		"\x1b]2;another\x1b\\",
		"\x1b]133;D;3\x07",
	}
	wantOut := "\x1b]0;window title\x07\x1b]2;another\x1b\\"

	run := func(t *testing.T, writes []string) {
		var out bytes.Buffer
		var got []Event
		p := NewParser(&out, func(e Event) { got = append(got, e) })
		for _, w := range writes {
			if _, err := p.Write([]byte(w)); err != nil {
				t.Fatalf("Write: unexpected error: %v", err)
			}
		}
		if err := p.Flush(); err != nil {
			t.Fatalf("Flush: unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("want exactly 2 events, got %d: %+v", len(got), got)
		}
		if got[0].Kind != PromptStart {
			t.Errorf("event 0: want PromptStart, got %s", got[0].Kind)
		}
		if got[1].Kind != CommandDone || got[1].ExitCode != 3 {
			t.Errorf("event 1: want CommandDone ExitCode 3, got %+v", got[1])
		}
		if out.String() != wantOut {
			t.Errorf("forwarded bytes: want %q, got %q", wantOut, out.String())
		}
	}

	t.Run("four writes", func(t *testing.T) { run(t, parts) })
	t.Run("one write", func(t *testing.T) { run(t, []string{strings.Join(parts, "")}) })
}

// TestParserHostileInput is AC5: hostile input never corrupts state, never
// panics, and never emits a bogus event. Every row is forwarded byte for
// byte because nothing here is a fully recognized marker.
func TestParserHostileInput(t *testing.T) {
	for _, c := range hostileCases() {
		run := func(t *testing.T, writes func(p *Parser, in string)) {
			var out bytes.Buffer
			var got []Event
			p := NewParser(&out, func(e Event) { got = append(got, e) })
			writes(p, c.in)
			if err := p.Flush(); err != nil {
				t.Fatalf("Flush: unexpected error: %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("want 0 events, got %d: %+v", len(got), got)
			}
			if out.String() != c.in {
				t.Errorf("forwarded bytes: want %q, got %q", c.in, out.String())
			}
		}

		t.Run(c.name+"/whole", func(t *testing.T) {
			run(t, func(p *Parser, in string) {
				if _, err := p.Write([]byte(in)); err != nil {
					t.Fatalf("Write: unexpected error: %v", err)
				}
			})
		})
		t.Run(c.name+"/one-byte", func(t *testing.T) {
			run(t, func(p *Parser, in string) {
				for i := 0; i < len(in); i++ {
					if _, err := p.Write([]byte{in[i]}); err != nil {
						t.Fatalf("Write: unexpected error: %v", err)
					}
				}
			})
		})
		for i := 1; i < len(c.in); i++ {
			i := i
			t.Run(fmt.Sprintf("%s/split-at-%d", c.name, i), func(t *testing.T) {
				run(t, func(p *Parser, in string) {
					if _, err := p.Write([]byte(in[:i])); err != nil {
						t.Fatalf("first Write: unexpected error: %v", err)
					}
					if _, err := p.Write([]byte(in[i:])); err != nil {
						t.Fatalf("second Write: unexpected error: %v", err)
					}
				})
			})
		}
	}
}

// TestParserPayloadCapOverflow: a payload that never terminates and exceeds
// MaxOSCPayload is abandoned. Every held byte is forwarded and the parser
// returns cleanly to Normal, ready to recognize the very next marker.
func TestParserPayloadCapOverflow(t *testing.T) {
	overflow := "\x1b]" + strings.Repeat("x", MaxOSCPayload+10) + "\x07"

	var out bytes.Buffer
	var got []Event
	p := NewParser(&out, func(e Event) { got = append(got, e) })

	if _, err := p.Write([]byte(overflow)); err != nil {
		t.Fatalf("Write: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 events from overflow, got %d: %+v", len(got), got)
	}
	if out.String() != overflow {
		t.Fatalf("forwarded bytes: want the overflow input verbatim, got a mismatch of length %d vs %d", out.Len(), len(overflow))
	}

	out.Reset()
	got = nil
	if _, err := p.Write([]byte("\x1b]133;A\x07")); err != nil {
		t.Fatalf("Write after overflow: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Kind != PromptStart {
		t.Fatalf("want exactly one PromptStart after overflow recovery, got %+v", got)
	}
	if out.Len() != 0 {
		t.Fatalf("want no further bytes forwarded after recovery, got %q", out.String())
	}
}

// TestParserRecoversAfterHostileSequence: every hostile row followed by a
// genuine marker must still yield exactly that marker's event, proving the
// hostile input left no residue in parser state.
func TestParserRecoversAfterHostileSequence(t *testing.T) {
	for _, c := range hostileCases() {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			var got []Event
			p := NewParser(&out, func(e Event) { got = append(got, e) })

			if _, err := p.Write([]byte(c.in)); err != nil {
				t.Fatalf("Write hostile: unexpected error: %v", err)
			}
			if _, err := p.Write([]byte("\x1b]133;D;42\x07")); err != nil {
				t.Fatalf("Write recovery marker: unexpected error: %v", err)
			}
			if err := p.Flush(); err != nil {
				t.Fatalf("Flush: unexpected error: %v", err)
			}

			if len(got) != 1 {
				t.Fatalf("want exactly 1 event, got %d: %+v", len(got), got)
			}
			if got[0].Kind != CommandDone || got[0].ExitCode != 42 {
				t.Fatalf("want CommandDone ExitCode 42, got %+v", got[0])
			}
			if out.String() != c.in {
				t.Errorf("forwarded bytes: want the hostile row alone, want %q, got %q", c.in, out.String())
			}
		})
	}
}

// TestParserFlushEmitsHeldBytes is AC8: a truncated sequence at stream end
// forwards nothing until Flush is called, and Flush emits it exactly.
//
// The plan this ticket follows states the pre-Flush expectation as "nothing
// at all is forwarded until Flush is called" for every row, including "text
// then bare esc". That is not achievable by a correct streaming
// implementation: the plan's own algorithm for the Normal state (an
// IndexByte scan that forwards a run of ordinary text the instant it finds
// the ESC that ends the run) forwards "hello" immediately, before the
// pending ESC is even resolved, and it must, because holding unambiguous
// plain text until Flush would add latency for a learner's ordinary output
// and contradicts the streaming requirement in internal/pty/doc.go and the
// component-traps skill. wantBeforeFlush corrects that one row rather than
// asserting a zero that only a buffering implementation could produce.
func TestParserFlushEmitsHeldBytes(t *testing.T) {
	cases := []struct {
		name            string
		in              string
		wantBeforeFlush string
	}{
		{"bare esc at end", "\x1b", ""},
		{"osc introducer only", "\x1b]", ""},
		{"partial recognized payload", "\x1b]133;", ""},
		{"complete payload no terminator", "\x1b]133;D;0", ""},
		{"esc pending inside payload", "\x1b]133;A\x1b", ""},
		{"text then bare esc", "hello\x1b", "hello"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			p := NewParser(&out, nil)
			if _, err := p.Write([]byte(c.in)); err != nil {
				t.Fatalf("Write: unexpected error: %v", err)
			}
			if out.String() != c.wantBeforeFlush {
				t.Fatalf("before Flush: want %q forwarded, got %q", c.wantBeforeFlush, out.String())
			}
			if err := p.Flush(); err != nil {
				t.Fatalf("Flush: unexpected error: %v", err)
			}
			if out.String() != c.in {
				t.Fatalf("after Flush: want %q, got %q", c.in, out.String())
			}
		})
	}
}

// TestParserFlushIsIdempotent: calling Flush repeatedly forwards nothing more
// after the first call, and Write after Flush resumes cleanly from Normal.
func TestParserFlushIsIdempotent(t *testing.T) {
	var out bytes.Buffer
	p := NewParser(&out, nil)

	if _, err := p.Write([]byte("\x1b]133;")); err != nil {
		t.Fatalf("Write: unexpected error: %v", err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("first Flush: unexpected error: %v", err)
	}
	afterFirst := out.String()

	for i := 0; i < 2; i++ {
		if err := p.Flush(); err != nil {
			t.Fatalf("Flush #%d: want nil error, got %v", i+2, err)
		}
		if out.String() != afterFirst {
			t.Fatalf("Flush #%d: forwarded additional bytes, want %q, got %q", i+2, afterFirst, out.String())
		}
	}

	var got []Event
	p2 := NewParser(&out, func(e Event) { got = append(got, e) })
	if err := p2.Flush(); err != nil {
		t.Fatalf("Flush on fresh parser: unexpected error: %v", err)
	}
	if _, err := p2.Write([]byte("\x1b]133;A\x07")); err != nil {
		t.Fatalf("Write after Flush: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Kind != PromptStart {
		t.Fatalf("want exactly one PromptStart after Flush, got %+v", got)
	}
}

// TestParserWriteReturnsInputLength: Write must report the count of INPUT
// bytes consumed, never the count of downstream bytes written. Getting this
// wrong makes io.Copy return io.ErrShortWrite in production.
func TestParserWriteReturnsInputLength(t *testing.T) {
	var out bytes.Buffer
	p := NewParser(&out, nil)

	for _, c := range recognizedCases() {
		n, err := p.Write([]byte(c.in))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		if n != len(c.in) {
			t.Errorf("%s: want n=%d, got n=%d", c.name, len(c.in), n)
		}
	}

	for _, c := range hostileCases() {
		n, err := p.Write([]byte(c.in))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		if n != len(c.in) {
			t.Errorf("%s: want n=%d, got n=%d", c.name, len(c.in), n)
		}
	}
}

var sentinelErr = errors.New("sentinel write failure")

// failingWriter forwards up to k bytes across all calls combined, then
// returns sentinelErr on every write after that.
type failingWriter struct {
	k       int
	written int
}

func (w *failingWriter) Write(b []byte) (int, error) {
	if w.written >= w.k {
		return 0, sentinelErr
	}
	remaining := w.k - w.written
	if remaining > len(b) {
		remaining = len(b)
	}
	w.written += remaining
	if remaining < len(b) {
		return remaining, sentinelErr
	}
	return remaining, nil
}

// TestParserWriterErrorPropagates checks the io.Writer contract end to end: n
// never exceeds len(b), a short n implies a non-nil error, the error is the
// sentinel wrapped, and it stays sticky across later calls.
func TestParserWriterErrorPropagates(t *testing.T) {
	for _, k := range []int{0, 1, 5} {
		t.Run(fmt.Sprintf("k=%d", k), func(t *testing.T) {
			fw := &failingWriter{k: k}
			p := NewParser(fw, nil)

			in := "\x1b]0;unrecognized title that is long enough to exceed five bytes\x07"
			n, err := p.Write([]byte(in))
			if err == nil {
				t.Fatalf("want a non-nil error, got nil (n=%d)", n)
			}
			if !errors.Is(err, sentinelErr) {
				t.Fatalf("want errors.Is(err, sentinelErr), got %v", err)
			}
			if n < 0 || n > len(in) {
				t.Fatalf("want 0 <= n <= %d, got %d", len(in), n)
			}
			if n < len(in) && err == nil {
				t.Fatalf("n < len(in) but err is nil")
			}

			n2, err2 := p.Write([]byte("more"))
			if n2 != 0 {
				t.Errorf("second Write: want n=0, got %d", n2)
			}
			if !errors.Is(err2, sentinelErr) {
				t.Errorf("second Write: want errors.Is(err2, sentinelErr), got %v", err2)
			}

			if err3 := p.Flush(); !errors.Is(err3, sentinelErr) {
				t.Errorf("Flush: want errors.Is(err3, sentinelErr), got %v", err3)
			}
		})
	}
}

// TestParserNilOnEventAndNilOut: a nil onEvent must not panic and must still
// strip recognized markers. A nil out must not panic and must still deliver
// events, discarding the forwarded bytes.
func TestParserNilOnEventAndNilOut(t *testing.T) {
	t.Run("nil onEvent", func(t *testing.T) {
		var out bytes.Buffer
		p := NewParser(&out, nil)
		if _, err := p.Write([]byte("\x1b]133;A\x07")); err != nil {
			t.Fatalf("Write: unexpected error: %v", err)
		}
		if err := p.Flush(); err != nil {
			t.Fatalf("Flush: unexpected error: %v", err)
		}
		if out.Len() != 0 {
			t.Fatalf("want the recognized marker stripped even with nil onEvent, got %q", out.String())
		}
	})

	t.Run("nil out", func(t *testing.T) {
		var got []Event
		p := NewParser(nil, func(e Event) { got = append(got, e) })
		if _, err := p.Write([]byte("\x1b]133;B\x07")); err != nil {
			t.Fatalf("Write: unexpected error: %v", err)
		}
		if err := p.Flush(); err != nil {
			t.Fatalf("Flush: unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].Kind != CommandStart {
			t.Fatalf("want exactly one CommandStart with nil out, got %+v", got)
		}
	})
}

// TestParserEventAtIsStamped confirms the clock contract: At is stamped from
// the parser's clock at the moment the terminator byte is consumed, and it is
// never the zero value.
func TestParserEventAtIsStamped(t *testing.T) {
	fixed := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	var got []Event
	p := NewParser(&out, func(e Event) { got = append(got, e) })
	p.now = func() time.Time { return fixed }

	if _, err := p.Write([]byte("\x1b]133;A\x07")); err != nil {
		t.Fatalf("Write: unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 event, got %d", len(got))
	}
	if !got[0].At.Equal(fixed) {
		t.Fatalf("want At=%v, got %v", fixed, got[0].At)
	}
}
