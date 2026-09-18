package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"golang.org/x/term"

	"github.com/JoottunAtish/ShellForge/internal/content"
)

// Rendering a level briefing.
//
// A briefing is markdown in the level YAML: `##` headings, backtick spans,
// blank-line paragraphs. It is the first thing a learner reads and it is
// supposed to feel like a story, so it gets rendered rather than dumped.
//
// The important difference from render_check.go: this runs BEFORE the shell is
// attached, so nothing here builds its own CRLF the way a check reply does.
// A check reply goes through crlf because by then internal/pty has taken the
// terminal. Getting those two the wrong way round is the easiest mistake
// available in this pair of files, which is why both say so.
//
// That used to be the whole story, on the grounds that a terminal with no
// shell attached to it yet still treats a plain "\n" as a newline. It does,
// once. `play` walks a campaign in one process, so every briefing after the
// first is printed into a terminal that has already hosted a session, and
// that terminal no longer returns the carriage on its own. hostWriter is
// where that is put right, and it is applied at the call site rather than
// here so that this renderer stays something a golden test can compare
// against ordinary Go string literals.

const (
	// Briefing width bounds. Narrower than 40 columns makes prose unreadable
	// whatever we do; wider than 100 makes long lines hard to track back to the
	// left margin. Between those, follow the terminal.
	minBriefWidth     = 40
	maxBriefWidth     = 100
	defaultBriefWidth = 80

	// briefingRule separates the briefing from the shell that follows, and is
	// the fallback presentation when markdown rendering is unavailable.
	briefingRule = "----------------------------------------------------------------------"
)

// printBriefing writes the rendered briefing and the objective checklist.
//
// The checklist prints every objective unchecked, because nothing has been
// verified yet. That is deliberate rather than lazy: a learner needs to see the
// full shape of the task before starting, and `check` is what fills it in.
func printBriefing(w io.Writer, level *content.Level, width int, color bool) {
	fmt.Fprintf(w, "\n%s\n", briefingRule)

	if title := strings.TrimSpace(level.Title); title != "" {
		fmt.Fprintf(w, "%s\n", title)
	}

	body := renderMarkdown(level.Briefing, width, color)
	fmt.Fprintf(w, "\n%s\n", strings.TrimRight(body, "\n"))

	printObjectiveChecklist(w, level)
	printCommandFooter(w)

	fmt.Fprintf(w, "%s\n\n", briefingRule)
}

// printObjectiveChecklist writes the unchecked objective list.
//
// A boss level shows no numbered steps, per docs/LEVEL-FORMAT.md section 2: the
// point of a boss is that the learner works out the shape themselves. The
// objectives are still listed, because they are what `check` verifies and hiding
// them would make the level unfair rather than hard; what is dropped is the
// step numbering that would read as instructions.
func printObjectiveChecklist(w io.Writer, level *content.Level) {
	if len(level.Objectives) == 0 {
		return
	}

	fmt.Fprintf(w, "\nWhat needs to be true when you are done:\n")
	for i, obj := range level.Objectives {
		label := obj.Text
		if obj.Optional {
			label += " (bonus)"
		}
		if level.Boss {
			fmt.Fprintf(w, "  [ ] %s\n", label)
			continue
		}
		fmt.Fprintf(w, "  %d. [ ] %s\n", i+1, label)
	}
}

// renderMarkdown turns a briefing into text for a terminal.
//
// On any failure it returns the markdown unchanged rather than reporting an
// error. Refusing to start a level because a markdown renderer choked would be
// indefensible: the raw markdown is perfectly readable, and a learner who cannot
// play because of a formatting library has been failed by us rather than by
// their answer. The caller logs the reason only under --log-level=debug.
// printCommandFooter writes the only list of in-level commands a learner
// ever sees, so it names all of them.
//
// It used to name `check` and `exit` alone, which is the quit button and the
// one thing a stuck learner has already tried. The hint ladder is 101 written
// hints across 25 levels, `brief` is the only way back to objectives that have
// scrolled off, and `reset` is the only way out of a level world the learner
// has broken. None of the three is discoverable anywhere else: no briefing in
// the pack mentions them, and `help` inside the sandbox reaches bash before it
// reaches us unless something claims the name.
//
// It is its own function rather than the last line of printObjectiveChecklist
// because that returns early on a level with no objectives, which left the
// learner told nothing at all.
func printCommandFooter(w io.Writer) {
	fmt.Fprintf(w, "\nType `check` when you think you have it, or `hint` if you are stuck.\n")
	fmt.Fprintf(w, "`brief` reprints this, `reset` rebuilds the level, and `exit` leaves.\n")
}

func renderMarkdown(md string, width int, color bool) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(briefingStyle(color)),
		glamour.WithWordWrap(clampBriefWidth(width)),
	)
	if err != nil {
		return md
	}

	out, err := renderer.Render(md)
	if err != nil {
		return md
	}
	// A renderer that returns nothing for non-empty input has failed without
	// saying so. Falling back is better than printing a blank briefing.
	if strings.TrimSpace(out) == "" {
		return md
	}
	return trimTrailingSpace(out)
}

// briefingStyle is this project's own glamour style, rather than one of the
// standard ones, and the reason is worth recording because reaching for
// WithStandardStyle is the obvious thing to do.
//
// Measured against nav-01's real briefing, 352 bytes of markdown:
//
//   - Every standard style, "ascii" and "notty" included, leaves the "##"
//     heading marker in the output. It strips backticks and wraps text, but a
//     learner still reads "## 08:15, Monday", which is the markdown showing
//     through rather than being rendered.
//   - The "dark" style turns those 352 bytes into 5,698 bytes with 669 escape
//     sequences, because it paints every padding space to the wrap width
//     individually. That is invisible on a terminal but it matters here: the
//     `brief` verb sends its reply back through the control FIFO, where a reply
//     is capped, so a long briefing under that style could be truncated for no
//     reason a learner could understand.
//
// This config produces 695 bytes with 10 escape sequences for the same input,
// with the heading marker gone and the heading bold instead. Headings get no
// prefix, code spans get one colour, and the document block carries no margin or
// background, which is where the per-space padding came from.
// When colour is off, NOTHING is styled: not bold, not italic either. Bold is
// technically not colour, but internal/platform/ux emits no escape at all when
// colour is suppressed, and the reasons colour is suppressed apply equally to
// bold. TERM=dumb means a terminal that cannot render attributes, and a
// redirected stream means bytes going into a file where an escape is noise. A
// briefing that emitted bold under NO_COLOR would be the one part of the program
// that ignored the setting.
func briefingStyle(color bool) ansi.StyleConfig {
	var heading, strong, emph, code ansi.StylePrimitive
	if color {
		// The same two roles internal/platform/ux uses, so a briefing and an
		// error message look like they came from one program.
		heading = ansi.StylePrimitive{Bold: boolPtr(true), Color: stringPtr("39")}
		strong = ansi.StylePrimitive{Bold: boolPtr(true)}
		emph = ansi.StylePrimitive{Italic: boolPtr(true)}
		code = ansi.StylePrimitive{Color: stringPtr("203")}
	}

	return ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{BlockPrefix: "", BlockSuffix: ""},
			Margin:         uintPtr(0),
		},
		Paragraph: ansi.StyleBlock{},
		Heading:   ansi.StyleBlock{StylePrimitive: heading},
		H1:        ansi.StyleBlock{StylePrimitive: heading},
		H2:        ansi.StyleBlock{StylePrimitive: heading},
		H3:        ansi.StyleBlock{StylePrimitive: heading},
		H4:        ansi.StyleBlock{StylePrimitive: heading},
		Code:      ansi.StyleBlock{StylePrimitive: code},
		Strong:    strong,
		Emph:      emph,
		List:      ansi.StyleList{LevelIndent: 2},
		Item:      ansi.StylePrimitive{BlockPrefix: "- "},
		Text:      ansi.StylePrimitive{},
	}
}

// trimTrailingSpace removes the padding glamour adds to the right of every line.
//
// It is invisible on a terminal, so this is not about appearance: it is about a
// briefing that goes into a bug report, a CI transcript or a golden test file
// carrying columns of trailing spaces that make a diff unreadable.
func trimTrailingSpace(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

// glamour's style config takes pointers for every optional field, so these three
// exist to keep briefingStyle readable rather than half address-of expressions.
func boolPtr(b bool) *bool       { return &b }
func stringPtr(s string) *string { return &s }
func uintPtr(u uint) *uint       { return &u }

// clampBriefWidth bounds a width into the readable range.
func clampBriefWidth(width int) int {
	switch {
	case width < minBriefWidth:
		return minBriefWidth
	case width > maxBriefWidth:
		return maxBriefWidth
	default:
		return width
	}
}

// terminalWidth reports the width to render at.
//
// A non-terminal writer, which is what a test, a pipe and a CI log all are, gets
// the default rather than an error: the briefing still has to be readable in a
// transcript.
// The interface rather than a *os.File assertion, so that a briefing going
// through hostWriter's CRLF translation is still measured against the real
// terminal. Asserting the concrete type made the wrapper fall back to 80
// columns, which is a visible change to every briefing on a wider window.
// Anything else exposing an Fd is harmless here: term.GetSize fails on a
// descriptor that is not a terminal and the fallback below catches it.
func terminalWidth(w io.Writer) int {
	f, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return defaultBriefWidth
	}
	width, _, err := term.GetSize(int(f.Fd()))
	if err != nil || width <= 0 {
		return defaultBriefWidth
	}
	return width
}

// hostWriter wraps w so that every line ending reaches the host terminal
// with a carriage return, and returns w untouched when it is not a terminal.
//
// A terminal that has hosted one level session stops returning the carriage
// on a bare "\n", and stays that way for the rest of the process: measured
// on Windows, where the staircase starts at the first thing printed after
// the shell ends and runs through the next level's briefing. internal/pty
// restores the raw mode it set, and cmd_run.go restores the console mode on
// top of that, and neither brings it back, so the carriage return has to be
// in the bytes.
//
// Only when w is a terminal. Redirected output is somebody's file or pipe
// and a stray "\r" in it is corruption: `shellforge play > log` should read
// the same on every platform, and the golden tests write to a buffer, which
// is not a terminal and so is left exactly as it was.
func hostWriter(w io.Writer) io.Writer {
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return w
	}
	return &crlfWriter{w: f, fd: f.Fd()}
}

// crlfWriter is hostWriter's translation, streaming rather than whole
// string, because it wraps a writer that is handed a line at a time.
//
// last carries the final byte across calls so that a "\r" ending one Write
// and a "\n" opening the next is left alone rather than doubled, which is
// the same idempotence crlf has within one string.
type crlfWriter struct {
	w    io.Writer
	fd   uintptr
	last byte
}

func (c *crlfWriter) Write(p []byte) (int, error) {
	out := make([]byte, 0, len(p)+len(p)/8)
	for _, ch := range p {
		if ch == '\n' && c.last != '\r' {
			out = append(out, '\r')
		}
		out = append(out, ch)
		c.last = ch
	}
	if _, err := c.w.Write(out); err != nil {
		return 0, err
	}
	// The count is of p, not of what went out: io.Writer's contract is how
	// much of the caller's input was consumed, and never more than len(p).
	return len(p), nil
}

// Fd reports the underlying terminal's descriptor, so that terminalWidth
// measures the real window through the wrapper.
func (c *crlfWriter) Fd() uintptr { return c.fd }
