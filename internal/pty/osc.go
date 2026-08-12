package pty

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MaxOSCPayload is the largest OSC payload the parser will hold. A stream that
// opens an OSC and never terminates it must not grow memory without bound, so a
// payload that reaches this size is abandoned: every held byte is forwarded and
// the parser returns to Normal.
const MaxOSCPayload = 4096

// EventKind identifies which semantic marker a recognized Event represents.
type EventKind int

// The five markers this parser recognizes. See docs/design/ARCHITECTURE.md
// section 4.6 and internal/pty/doc.go: no others are in scope for this
// ticket.
const (
	PromptStart  EventKind = iota // OSC 133;A
	CommandStart                  // OSC 133;B
	PreExec                       // OSC 133;C
	CommandDone                   // OSC 133;D;<exit>
	CwdReport                     // OSC 7;file://<host><path>
)

// String returns a lowercase name for k, or a fallback for any value outside
// the five declared constants. It exists so a table test failure reads as
// "want commanddone, got cwdreport" instead of "want 3, got 4".
func (k EventKind) String() string {
	switch k {
	case PromptStart:
		return "promptstart"
	case CommandStart:
		return "commandstart"
	case PreExec:
		return "preexec"
	case CommandDone:
		return "commanddone"
	case CwdReport:
		return "cwdreport"
	default:
		return fmt.Sprintf("eventkind(%d)", int(k))
	}
}

// Event is a single recognized marker, delivered synchronously from Write at
// the moment its terminator byte is consumed.
//
// A byte-identical forged marker is indistinguishable from a genuine one: a
// learner who types printf '\e]133;D;0\a' produces an Event this parser
// cannot tell apart from a real command completion. No parser can fix this.
// Nothing in scoring or verification may treat a journal exit code as proof
// of anything; verification checks state, not the journal. See the follow-up
// noted against the journal and verify tickets.
type Event struct {
	Kind     EventKind
	ExitCode int // CommandDone only

	// Cwd, on a CwdReport event, is percent-decoded, begins with "/", and
	// contains no C0 control byte (0x00-0x1f) and no DEL (0x7f). A payload
	// that would decode to any of those, whether through a percent escape
	// such as "%0d" or "%1b", or through a raw ESC smuggled into the
	// payload ahead of decoding, is rejected outright: recognize returns
	// false, so no Event is emitted and the bytes are forwarded as an
	// unrecognized OSC sequence instead. The next layer can rely on this
	// without checking it again.
	Cwd string

	At time.Time // stamped when the terminator byte is consumed
}

// state is the parser's position in the escape sequence grammar.
//
// The ticket names five states, Normal -> ESC -> OSC -> OSC_PAYLOAD ->
// terminator. OSC and OSC_PAYLOAD collapse into stateOSC here because ] is
// the final introducer byte and there is no intermediate byte between it and
// the payload, so no state went missing.
type state int

const (
	stateNormal state = iota // outside any escape sequence
	stateEsc                 // ESC consumed, the next byte decides
	stateOSC                 // inside an OSC payload

	// stateOSCEsc is ESC consumed inside a payload, the next byte decides ST
	// or payload. This is the state that makes the cross-Write boundary case
	// correct: when a chunk ends immediately after an ESC inside a payload,
	// the ESC is not yet in payload and has not been written. The pending
	// decision IS this state, and it survives untouched to the next Write or
	// to Flush. There is no lookahead buffer and no deferred byte to lose,
	// because a peek across a Write boundary is impossible: the next byte
	// simply has not arrived yet.
	stateOSCEsc
)

// Parser is a streaming byte-level state machine that recognizes shellforge's
// OSC 133 and OSC 7 markers in a raw PTY byte stream, strips exactly the
// bytes it recognizes, and forwards everything else unmodified to out.
//
// Parser is not safe for concurrent use. It is driven by the single PTY read
// goroutine; add a lock at the caller if that ever changes rather than here,
// because a lock on this type would misstate the contract to every future
// reader.
type Parser struct {
	out     io.Writer
	onEvent func(Event)
	now     func() time.Time

	st      state
	payload []byte
	werr    error
}

// NewParser returns a Parser that forwards unrecognized bytes to out and
// reports recognized markers to onEvent.
//
// A nil out is allowed and is treated as io.Discard: recognized markers are
// still stripped and delivered to onEvent, and the forwarded bytes are
// discarded. A nil onEvent is allowed and is treated as a no-op: recognized
// markers are still stripped from the forwarded stream, they are simply not
// reported. Neither nil value is an error.
//
// onEvent is called synchronously from Write. It must not block and must not
// call back into this Parser; reentrancy is undefined and unguarded.
func NewParser(out io.Writer, onEvent func(Event)) *Parser {
	if out == nil {
		out = io.Discard
	}
	return &Parser{
		out:     out,
		onEvent: onEvent,
		now:     time.Now,
	}
}

// Write consumes b, recognizing and stripping shellforge's OSC markers and
// forwarding every other byte to the Parser's writer unmodified.
//
// n counts INPUT bytes consumed, never downstream bytes written: 0 <= n <=
// len(b), and n < len(b) implies a non-nil err. That is exactly io.Writer's
// contract, so a caller such as io.Copy never fabricates io.ErrShortWrite. A
// write error is sticky: once set, every later call to Write or Flush
// returns it and forwards nothing further.
func (p *Parser) Write(b []byte) (int, error) {
	i := 0
	for i < len(b) {
		if p.werr != nil {
			return i, p.werr
		}
		if p.st == stateNormal {
			j := bytes.IndexByte(b[i:], 0x1b)
			if j < 0 {
				if !p.forward(b[i:]) {
					return i, p.werr
				}
				return len(b), nil
			}
			if j > 0 && !p.forward(b[i:i+j]) {
				return i, p.werr
			}
			i += j + 1 // consume the ESC without writing it
			p.st = stateEsc
			continue
		}
		if p.step(b[i]) {
			i++
		}
	}
	return i, p.werr
}

// step processes a single byte against the Parser's current non-Normal
// state. It returns true when the byte was consumed and false when the byte
// must be re-dispatched: the current state transitioned on account of a
// held byte, and the byte just given to step has not been accounted for yet.
//
// Termination proof: the only non-consuming transitions are stateEsc with a
// non-']' byte, stateOSC on overflow, and stateOSCEsc with a non-backslash
// byte. Each of those sets a state that will consume the byte on the very
// next call, so at most two non-consuming steps occur per input byte and
// this cannot spin.
func (p *Parser) step(b byte) bool {
	switch p.st {
	case stateEsc:
		if b == ']' {
			p.st = stateOSC
			p.payload = p.payload[:0]
			return true
		}
		// The held ESC was not the start of an OSC sequence after all.
		// Write it as a single byte and re-dispatch the current byte in
		// Normal: this is what makes ESC [ CSI traffic and ESC ESC come
		// out byte for byte.
		p.forward([]byte{0x1b})
		p.st = stateNormal
		return false

	case stateOSC:
		switch b {
		case 0x07: // BEL
			p.resolve([]byte{0x07})
			p.st = stateNormal
			return true
		case 0x1b:
			p.st = stateOSCEsc
			return true
		default:
			if len(p.payload) == MaxOSCPayload {
				p.overflow()
				return false
			}
			p.payload = append(p.payload, b)
			return true
		}

	case stateOSCEsc:
		if b == '\\' {
			p.resolve([]byte{0x1b, '\\'})
			p.st = stateNormal
			return true
		}
		// The held ESC was payload after all: it did not introduce an ST
		// terminator. Append it to payload under the same cap rule that
		// governs every other payload byte, and re-dispatch the current
		// byte in stateOSC. It may itself be another ESC, or a BEL that
		// terminates, or an ordinary payload byte, and re-dispatching is
		// the only way all three stay correct.
		if len(p.payload) == MaxOSCPayload {
			p.overflowWithHeldEsc()
			return false
		}
		p.payload = append(p.payload, 0x1b)
		p.st = stateOSC
		return false

	default:
		// stateNormal is handled entirely in Write; step is never called
		// with it.
		return true
	}
}

// resolve is called with the terminator bytes that just closed an OSC
// payload. A recognized payload is stripped and reported as an Event with
// no bytes forwarded. An unrecognized payload is forwarded whole, including
// the introducer and the terminator it actually arrived with: taking term as
// a parameter rather than assuming BEL is load-bearing for byte identity, so
// an unrecognized ST-terminated sequence comes out with the ST it arrived
// with.
func (p *Parser) resolve(term []byte) {
	if ev, ok := recognize(p.payload); ok {
		ev.At = p.now()
		if p.onEvent != nil {
			p.onEvent(ev)
		}
		p.payload = p.payload[:0]
		return
	}
	p.forward([]byte{0x1b, ']'})
	p.forward(p.payload)
	p.forward(term)
	p.payload = p.payload[:0]
}

// overflow abandons a payload that reached MaxOSCPayload while in stateOSC.
// Held bytes go out before the byte that triggered the overflow, or byte
// order breaks. Nothing is stripped, no event is emitted, and no error is
// returned: a stream that opens an OSC and never closes it is a misbehaving
// producer, not a user error.
func (p *Parser) overflow() {
	p.forward([]byte{0x1b, ']'})
	p.forward(p.payload)
	p.payload = p.payload[:0]
	p.st = stateNormal
}

// overflowWithHeldEsc abandons a payload that reached MaxOSCPayload while an
// ESC was held in stateOSCEsc, deciding whether it introduced ST. That held
// ESC must be forwarded too, or a byte silently vanishes. Handled as its own
// branch rather than falling through overflow, which would drop it.
func (p *Parser) overflowWithHeldEsc() {
	p.forward([]byte{0x1b, ']'})
	p.forward(p.payload)
	p.forward([]byte{0x1b})
	p.payload = p.payload[:0]
	p.st = stateNormal
}

// recognize inspects a completed OSC payload and reports the Event it names,
// if any. Recognition is exact: "133;A", "133;B", and "133;C" match by exact
// string equality, not prefix, so "133;A;aid=7" is not recognized and its
// bytes are forwarded. A future marker with parameters is out of scope for
// this ticket.
//
// TODO(v0.2): if instrument.bash ever grows parameterized 133;A/B/C forms,
// extend recognition here rather than widening this to a prefix match.
func recognize(payload []byte) (Event, bool) {
	switch string(payload) {
	case "133;A":
		return Event{Kind: PromptStart}, true
	case "133;B":
		return Event{Kind: CommandStart}, true
	case "133;C":
		return Event{Kind: PreExec}, true
	}

	if bytes.HasPrefix(payload, []byte("133;D;")) {
		rest := payload[len("133;D;"):]
		if len(rest) == 0 || !allDigits(rest) {
			return Event{}, false
		}
		n, err := strconv.Atoi(string(rest))
		// A wait status is 0-255: 0-127 for a normal exit and 128+signal for
		// a death by signal. Anything larger did not come from a shell, so
		// it is forwarded as unrecognized rather than reported as a
		// completion.
		if err != nil || n > 255 {
			return Event{}, false
		}
		return Event{Kind: CommandDone, ExitCode: n}, true
	}

	if bytes.HasPrefix(payload, []byte("7;file://")) {
		rest := payload[len("7;file://"):]
		i := bytes.IndexByte(rest, '/')
		if i < 0 {
			return Event{}, false
		}
		// The host, rest[:i], is ignored entirely: any host is accepted,
		// including an empty one and one that is not "quest". The path is
		// the only part any consumer uses, and instrument.bash hardcodes
		// "quest" today while the WSL backend may report a different
		// hostname.
		//
		// Wire format contract: an OSC 7 payload is a file:// URI, and a URI
		// is percent-encoded by definition, so the producer MUST
		// percent-encode the path before writing it here. This decoding side
		// is correct and does not change to accommodate a producer that gets
		// it wrong. images/rc/instrument.bash now meets that contract:
		// __sf_after calls __sf_urlencode on $PWD before writing it into the
		// payload (issue #44, superseding #25). __sf_urlencode encodes every
		// byte outside the RFC 3986 unreserved set as %XX before $PWD reaches
		// this payload, except '/', which stays a raw path separator.
		//
		// A raw-path fallback was proposed and rejected while this mismatch
		// stood, because it would make a directory literally named "a%20b"
		// permanently ambiguous with a directory named "a b". That reasoning
		// is exactly why the fix above works: with the producer encoding
		// correctly, "a%20b" and "a b" encode to two different payloads and
		// stay distinguishable. See images/rc/instrument_test.bash for the
		// round-trip cases, including that one.
		//
		// url.PathUnescape, not url.QueryUnescape: QueryUnescape maps '+' to
		// a space and would rename a directory literally called "a+b" to
		// "a b", silently wrong.
		dec, err := url.PathUnescape(string(rest[i:]))
		if err != nil {
			return Event{}, false
		}
		// dec reaches L3 and L4 unexamined otherwise. A percent escape can
		// decode to anything: "%0d%1b[2J" decodes to a CR followed by an
		// ESC-introduced CSI clear, and "%00" decodes to a NUL. A raw ESC
		// can also arrive with no encoding at all, because the
		// stateOSCEsc re-dispatch appends a non-ST ESC straight into the
		// payload. Reject rather than sanitize: the caller should not be
		// handed a "cleaned" path that a shell would have rejected too.
		if strings.ContainsFunc(dec, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return Event{}, false
		}
		return Event{Kind: CwdReport, Cwd: dec}, true
	}

	return Event{}, false
}

// allDigits reports whether every byte in b is an ASCII decimal digit. This
// is a pre-check ahead of strconv.Atoi, and it is what rejects a leading '+',
// a leading '-', a leading space, and an underscore digit separator, none of
// which strconv.Atoi rejects on its own for every Go version.
func allDigits(b []byte) bool {
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// forward writes b to the Parser's writer, unless a previous write already
// failed. It is the only place p.out.Write is called. It returns false the
// moment a write fails or once a previous write has already failed, which
// callers use to unwind out of Write early.
func (p *Parser) forward(b []byte) bool {
	if p.werr != nil {
		return false
	}
	if len(b) == 0 {
		return true
	}
	if _, err := p.out.Write(b); err != nil {
		p.werr = fmt.Errorf("forward pty output: %w", err)
		return false
	}
	return true
}

// Flush forwards any bytes held for a truncated sequence at stream end and
// returns the Parser to state Normal.
//
// Flush may be called any number of times. The first call forwards the held
// bytes; every later call forwards nothing and returns the same sticky
// error, if any. Write after Flush is fully defined and resumes from Normal.
func (p *Parser) Flush() error {
	switch p.st {
	case stateEsc:
		p.forward([]byte{0x1b})
	case stateOSC:
		p.forward([]byte{0x1b, ']'})
		p.forward(p.payload)
	case stateOSCEsc:
		p.forward([]byte{0x1b, ']'})
		p.forward(p.payload)
		p.forward([]byte{0x1b})
	}
	p.payload = p.payload[:0]
	p.st = stateNormal
	return p.werr
}
