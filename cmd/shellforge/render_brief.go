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
// attached, so the host terminal is not in raw mode yet and a plain "\n" is
// still a newline. Nothing here goes through crlf. A check reply does, because by
// then internal/pty has taken the terminal. Getting those two the wrong way
// round is the easiest mistake available in this pair of files, which is why both
// say so.

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
	fmt.Fprintf(w, "\nType `check` when you think you have it. Type `exit` to leave.\n")
}

// renderMarkdown turns a briefing into text for a terminal.
//
// On any failure it returns the markdown unchanged rather than reporting an
// error. Refusing to start a level because a markdown renderer choked would be
// indefensible: the raw markdown is perfectly readable, and a learner who cannot
// play because of a formatting library has been failed by us rather than by
// their answer. The caller logs the reason only under --log-level=debug.
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
func terminalWidth(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return defaultBriefWidth
	}
	width, _, err := term.GetSize(int(f.Fd()))
	if err != nil || width <= 0 {
		return defaultBriefWidth
	}
	return width
}
