package main

import (
	"fmt"
	"strings"

	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// Rendering a check result for the learner's terminal.
//
// Everything here is a pure function over a verify.LevelResult, which is what
// makes it testable with golden strings and no sandbox. The engine decides what
// happened; this file decides what the learner reads.
//
// Two constraints shape every line.
//
// The reply travels back through the sandbox: the host writes it to a FIFO, a
// `cat` inside the container prints it, and it reaches the terminal through the
// PTY that internal/pty is holding in RAW MODE. Raw mode turns off the line
// discipline that would otherwise supply a carriage return, so a bare "\n"
// renders as a staircase. Every line therefore ends "\r\n", and crlf is the one
// place that is applied.
//
// Only the first failing required objective's on_fail is shown, per
// docs/LEVEL-FORMAT.md section 4 rule 5. Printing five messages when the second
// follows from the first is how a beginner concludes the game is broken rather
// than their answer.

const (
	// The status marks. ASCII rather than Unicode check marks, for three
	// reasons: the punctuation gate bans the typographic characters that would
	// be the obvious choice, a golden test can grep for them, and a learner on
	// a terminal with a partial font sees them rather than replacement boxes.
	markPass    = "[ok]"
	markFail    = "[xx]"
	markBonus   = "[..]"
	markUnknown = "[!!]"

	// maxReplyBytes caps a reply.
	//
	// on_fail text comes from pack YAML, and the script check appends a failing
	// script's whole stdout to it. A level that printed a megabyte would flood
	// the learner's terminal and, because the reply crosses a FIFO, could wedge
	// the control channel behind a write nobody drains. Truncating is the
	// bounded failure; the level is still playable.
	maxReplyBytes = 16 * 1024

	truncationNotice = "... (output truncated)"
)

// ansi codes, used only when colour is enabled. They match the three roles
// internal/platform/ux already uses, so a check reply and an error message do
// not look like they came from different programs.
const (
	ansiReset = "\x1b[0m"
	ansiGood  = "\x1b[1;32m"
	ansiBad   = "\x1b[1;31m"
	ansiDim   = "\x1b[2m"
)

// renderCheckReply turns a level result into what the learner sees after typing
// `check`.
//
// color is decided once per session by ux.ColorEnabled and passed in, rather
// than asked here. By the time this runs there is no host *os.File to ask: the
// bytes are about to be handed to the sandbox. Colour survives the round trip
// because every hop is byte transparent.
//
// The returned string is already CRLF terminated and ready to write.
func renderCheckReply(res verify.LevelResult, color bool) string {
	p := palette(color)
	var b strings.Builder

	b.WriteString("\n")

	for _, obj := range res.Objectives {
		b.WriteString("  ")
		b.WriteString(p.mark(obj))
		b.WriteString(" ")
		b.WriteString(objectiveLine(obj))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(summaryLine(res, p))
	b.WriteString("\n")

	// The one on_fail the learner is shown. A second failing required
	// objective still got its [xx] line above, with no body.
	if res.PrimaryFailure != nil && res.PrimaryFailure.Message != "" {
		b.WriteString("\n")
		b.WriteString(indentParagraph(res.PrimaryFailure.Message))
		b.WriteString("\n")
	}

	if len(res.Notes) > 0 {
		b.WriteString("\n")
		b.WriteString(p.dim("Notes:"))
		b.WriteString("\n")
		for _, note := range res.Notes {
			b.WriteString(indentParagraph(note))
			b.WriteString("\n")
		}
	}

	return crlf(truncateReply(b.String()))
}

// renderCheckError is what the learner reads when the check could not be run at
// all, as opposed to having failed.
//
// It says their work is safe, because that is the first thing somebody wonders
// when a tool reports an internal problem in the middle of a level.
func renderCheckError(err error, levelID string) string {
	var b strings.Builder
	b.WriteString("\ncheck could not run: ")
	b.WriteString(err.Error())
	b.WriteString("\n\nYour files are safe. Type `exit`, then run `shellforge run ")
	b.WriteString(levelID)
	b.WriteString("` again.\n")
	return crlf(truncateReply(b.String()))
}

// objectiveLine is one checklist line: the authored text, plus a marker on a
// bonus so a learner does not think they failed something required.
func objectiveLine(obj verify.ObjectiveResult) string {
	line := obj.Text
	if line == "" {
		// The engine falls back to the id when a level supplied no text, so
		// this is belt and braces; an empty line would render as a bare mark.
		line = obj.ID
	}
	if obj.Optional {
		line += " (bonus)"
	}
	switch obj.Status {
	case verify.StatusTimeout:
		line += " (timed out)"
	case verify.StatusError:
		line += " (could not be checked)"
	}
	return line
}

// summaryLine is the verdict, and for a non-pass it is where the distinction
// between a wrong answer and an inconclusive run is made in words.
func summaryLine(res verify.LevelResult, p colours) string {
	if res.Passed {
		return p.good(fmt.Sprintf("PASS: %s", passSummary(res)))
	}

	if res.PrimaryFailure != nil {
		switch res.PrimaryFailure.Status {
		case verify.StatusTimeout:
			return p.bad("NOT YET: a check ran out of time, so this could not be decided. " +
				"That is not a wrong answer.")
		case verify.StatusError:
			return p.bad("NOT YET: a check could not determine an answer. " +
				"That is not a wrong answer.")
		}
	}
	return p.bad("NOT YET: " + failSummary(res))
}

// passSummary counts what was achieved, including bonuses, because a learner who
// missed a bonus has passed and should be told both facts at once.
func passSummary(res verify.LevelResult) string {
	var required, bonusEarned, bonusTotal int
	for _, obj := range res.Objectives {
		if obj.Optional {
			bonusTotal++
			if obj.Status == verify.StatusPass {
				bonusEarned++
			}
			continue
		}
		required++
	}

	summary := fmt.Sprintf("%s done", plural(required, "objective"))
	if bonusTotal > 0 {
		summary += fmt.Sprintf(", %d of %s", bonusEarned, plural(bonusTotal, "bonus"))
	}
	return summary
}

// failSummary counts what is left, so the learner knows whether they are one
// step away or several.
func failSummary(res verify.LevelResult) string {
	var done, outstanding int
	for _, obj := range res.Objectives {
		if obj.Optional {
			continue
		}
		if obj.Status == verify.StatusPass {
			done++
			continue
		}
		outstanding++
	}
	if done == 0 {
		return fmt.Sprintf("%s to go", plural(outstanding, "objective"))
	}
	return fmt.Sprintf("%d of %s done, %d to go", done, plural(done+outstanding, "objective"), outstanding)
}

// indentParagraph indents every line of a multi-line authored message by two
// spaces, so it reads as attached to the checklist above it.
//
// Carriage returns are stripped before splitting. An author on Windows can put
// CRLF into a YAML block scalar, and crlf would then turn "\r\n" into "\r\r\n",
// which a terminal renders as a stray blank column.
func indentParagraph(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

// truncateReply bounds the reply, cutting at a line boundary so the last thing
// the learner reads is a whole line rather than half a word.
func truncateReply(s string) string {
	if len(s) <= maxReplyBytes {
		return s
	}
	cut := s[:maxReplyBytes]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i]
	}
	return cut + "\n" + truncationNotice + "\n"
}

// crlf gives every line ending a carriage return, and is idempotent: a "\n"
// already preceded by "\r" is left alone.
//
// This is the single place the raw-mode rule is enforced. Everything upstream
// builds with plain "\n", which keeps the renderers readable and means a golden
// test can compare against ordinary Go string literals.
func crlf(s string) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' && (i == 0 || s[i-1] != '\r') {
			b.WriteByte('\r')
		}
		b.WriteByte(c)
	}
	return b.String()
}

// colours wraps text in ANSI codes, or does not.
type colours struct {
	enabled bool
}

func palette(enabled bool) colours { return colours{enabled: enabled} }

func (c colours) good(s string) string { return c.wrap(ansiGood, s) }
func (c colours) bad(s string) string  { return c.wrap(ansiBad, s) }
func (c colours) dim(s string) string  { return c.wrap(ansiDim, s) }

func (c colours) wrap(code, s string) string {
	if !c.enabled || s == "" {
		return s
	}
	return code + s + ansiReset
}

// mark returns the coloured status mark for one objective.
func (c colours) mark(obj verify.ObjectiveResult) string {
	switch obj.Status {
	case verify.StatusPass:
		return c.good(markPass)
	case verify.StatusTimeout, verify.StatusError:
		return c.bad(markUnknown)
	default:
		if obj.Optional {
			// A missed bonus is dim rather than red. It is not a failure, and
			// colouring it like one tells the learner they got something wrong
			// when they did not.
			return c.dim(markBonus)
		}
		return c.bad(markFail)
	}
}
