package pty

import (
	"bytes"
	"strings"
)

// maxCSIParams bounds the parameter bytes csiScanner will hold for one
// sequence. Every sequence it recognizes is under six bytes; anything past
// this bound is something else entirely, or a stream the learner is
// generating by hand, and is abandoned. Abandoning costs a missed nicety and
// nothing more: this scanner never writes, never consumes, and never alters
// a byte, so a missed sequence cannot corrupt anything.
const maxCSIParams = 32

// csiState is the scanner's position in the CSI grammar.
type csiState int

const (
	csiNone   csiState = iota // outside any escape sequence
	csiEsc                    // ESC consumed, the next byte decides
	csiParams                 // inside the parameter bytes of a CSI sequence
)

// csiScanner watches the shell's output stream for the one thing the game
// needs to know about it: the learner cleared their screen, so the briefing
// they were given at the start of the level is gone.
//
// It is a passive observer bolted onto the side of screen.Write. It reads
// the bytes on their way past and reports what it saw; it does not consume
// them, hold them, reorder them, or change one of them, because the OSC
// parser upstream has already promised that everything but this project's
// own markers reaches the terminal unmodified.
//
// Telling a real clear from vim's is what the alternate screen buffer is
// for. `clear`, and readline's own Ctrl-L, erase the primary screen. vim,
// less, man and htop switch to the alternate screen first (DECSET 1049, or
// the older 47 and 1047), erase that, and switch back on exit, which is why
// quitting them restores what was on screen before. So an erase that
// happens while the alternate buffer is active is a full screen program
// painting itself and is none of the game's business; an erase on the
// primary screen is the learner clearing theirs. That distinction is the
// whole reason this type exists rather than a bytes.Contains for "\x1b[2J".
//
// It is a streaming state machine for the same reason internal/pty's OSC
// parser is one: escape sequences arrive split across Read calls at
// arbitrary offsets, and a scanner that assumed otherwise would miss
// roughly one in a hundred, often enough to reach a learner and rarely
// enough to cost a day finding.
//
// Not safe for concurrent use. It is driven only from screen.Write, under
// screen's own mutex.
type csiScanner struct {
	st     csiState
	params []byte

	// inAlt tracks whether the alternate screen buffer is active. A count
	// rather than a bool would be wrong: the modes are set and reset, not
	// pushed and popped, and a program that resets one it never set (a
	// reset shell startup does exactly this) must leave the primary screen
	// selected rather than driving a counter negative.
	inAlt bool
}

// scan feeds p through the scanner and reports whether the learner's own
// screen, the primary one, was erased in full anywhere in it.
//
// Recognized: ED with parameter 2 (erase the whole display) or 3 (erase the
// scrollback), in both their ANSI and DEC private spellings, and the three
// alternate screen modes in both directions. Everything else is passed over.
func (c *csiScanner) scan(p []byte) (cleared bool) {
	for i := 0; i < len(p); {
		switch c.st {
		case csiNone:
			// memchr to the next ESC rather than a byte at a time: this
			// runs over every byte the sandbox writes to the terminal,
			// including a vim session redrawing at speed, and the common
			// case is a chunk with no escape in it at all.
			j := bytes.IndexByte(p[i:], 0x1b)
			if j < 0 {
				return cleared
			}
			i += j + 1
			c.st = csiEsc

		case csiEsc:
			switch p[i] {
			case '[':
				c.st = csiParams
				c.params = c.params[:0]
			case 0x1b:
				// ESC ESC: the second one is the introducer that counts.
			default:
				c.st = csiNone
			}
			i++

		case csiParams:
			b := p[i]
			i++
			if b >= 0x40 && b <= 0x7e {
				if c.classify(b) {
					cleared = true
				}
				c.st = csiNone
				continue
			}
			if len(c.params) == maxCSIParams {
				c.st = csiNone
				continue
			}
			c.params = append(c.params, b)
		}
	}
	return cleared
}

// classify acts on one complete CSI sequence, given its final byte, and
// reports whether it erased the primary screen.
func (c *csiScanner) classify(final byte) (cleared bool) {
	params := string(c.params)

	switch final {
	case 'J':
		// Parameters 0 and 1, and the empty default, erase only part of the
		// display and leave the briefing wherever it already was.
		switch params {
		case "2", "3", "?2", "?3":
			return !c.inAlt
		}
		return false

	case 'h', 'l':
		if !strings.HasPrefix(params, "?") {
			return false
		}
		for _, mode := range strings.Split(params[1:], ";") {
			switch mode {
			case "47", "1047", "1049":
				c.inAlt = final == 'h'
			}
		}
		return false
	}
	return false
}
