package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/content"
)

// briefingLevel is a level whose briefing exercises the markdown a real pack
// uses: a heading, a backtick span, emphasis and two paragraphs.
func briefingLevel() *content.Level {
	return &content.Level{
		ID:    "nav-01",
		Title: "First Contact",
		Briefing: "## 08:15, Monday. Your first day.\n\n" +
			"Somebody sat you at a desk and walked off to a meeting.\n\n" +
			"There is a folder called `quest` in your home directory. That is **where your work goes**.\n",
		Objectives: []content.Objective{
			{ID: "location", Text: "quest/answer.txt holds the folder you are standing in"},
			{ID: "used-pwd", Text: "Found it with a single command", Optional: true},
		},
	}
}

// TestPrintBriefingRendersMarkdownAndPrintsTheChecklist is the acceptance
// criterion: the briefing renders, and the objective checklist appears before
// the shell attaches.
//
// The checklist assertion is about ordering in the output, which is what the
// learner experiences as ordering in time: printBriefing is called before
// Attach, so everything it writes is on screen before the prompt.
func TestPrintBriefingRendersMarkdownAndPrintsTheChecklist(t *testing.T) {
	var buf bytes.Buffer
	printBriefing(&buf, briefingLevel(), 80, true)
	out := buf.String()

	// Rendered, not dumped: the markdown syntax is gone and ANSI is present.
	if strings.Contains(out, "## 08:15") {
		t.Errorf("the heading was printed as raw markdown:\n%s", out)
	}
	if strings.Contains(out, "**where your work goes**") {
		t.Errorf("emphasis was printed as raw markdown:\n%s", out)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("no ANSI in a coloured briefing, so nothing was rendered:\n%s", out)
	}

	// The words survive, which is the point of rendering rather than stripping.
	for _, want := range []string{"08:15", "quest", "walked off to a meeting"} {
		if !strings.Contains(stripANSI(out), want) {
			t.Errorf("the briefing lost %q:\n%s", want, out)
		}
	}

	// Both objectives are listed, unchecked, and the bonus is labelled.
	plain := stripANSI(out)
	for _, want := range []string{
		"quest/answer.txt holds the folder you are standing in",
		"Found it with a single command (bonus)",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("the checklist is missing %q:\n%s", want, plain)
		}
	}
	if strings.Count(plain, "[ ]") != 2 {
		t.Errorf("expected two unchecked objectives, got %d:\n%s", strings.Count(plain, "[ ]"), plain)
	}
	if strings.Contains(plain, "[ok]") || strings.Contains(plain, "[xx]") {
		t.Errorf("the briefing marked an objective as already checked:\n%s", plain)
	}
	if !strings.Contains(plain, "Type `check`") {
		t.Errorf("the briefing never tells the learner how to submit:\n%s", plain)
	}
}

// TestBriefingUsesPlainNewlinesNotCRLF is the counterpart to
// TestEveryReplyLineEndsCRLF, and the pair is the reason both files carry the
// same warning.
//
// A briefing is written before internal/pty puts the host terminal into raw
// mode, so the line discipline still supplies the carriage return. Emitting CRLF
// here would be wrong in the other direction, and the two mistakes look
// identical in a diff.
func TestBriefingUsesPlainNewlinesNotCRLF(t *testing.T) {
	var buf bytes.Buffer
	printBriefing(&buf, briefingLevel(), 80, false)

	if strings.Contains(buf.String(), "\r") {
		t.Errorf("the briefing contains a carriage return; it prints before raw mode and must use plain newlines:\n%q", buf.String())
	}
}

// TestBriefingHasNoColourWhenNotWanted is the NO_COLOR contract for the other
// renderer. glamour's notty style is asked for rather than colour being
// generated and stripped, because stripping would leave the layout decisions
// colour implies.
func TestBriefingHasNoColourWhenNotWanted(t *testing.T) {
	var buf bytes.Buffer
	printBriefing(&buf, briefingLevel(), 80, false)

	if strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("escape codes leaked into an uncoloured briefing:\n%q", buf.String())
	}
	// And the content is still all there.
	for _, want := range []string{"08:15", "quest", "answer.txt"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the uncoloured briefing lost %q:\n%s", want, buf.String())
		}
	}
}

// TestRenderMarkdownFallsBackRatherThanFailing is the rule that a formatting
// library must never stop a learner from playing.
//
// The realistic failure is a renderer that returns an error or empty output for
// input it cannot handle. Both paths return the markdown unchanged, which is
// perfectly readable, so the level still starts.
func TestRenderMarkdownFallsBackRatherThanFailing(t *testing.T) {
	// An empty briefing is the one input that legitimately renders to nothing.
	if got := renderMarkdown("", 80, false); got != "" {
		t.Errorf("renderMarkdown(\"\") = %q, want empty", got)
	}

	// Pathological but not malformed: markdown has no syntax errors, so the way
	// to exercise the fallback is content the renderer may refuse to wrap. What
	// matters is that whatever comes back contains the author's words.
	weird := "```\nunclosed fence\n"
	got := renderMarkdown(weird, 80, false)
	if !strings.Contains(stripANSI(got), "unclosed fence") {
		t.Errorf("renderMarkdown lost the author's text on odd input: %q", got)
	}
}

// TestBriefingRendersHeadingMarkersAway pins the first of the two reasons this
// project carries its own glamour style rather than using a standard one.
//
// Every standard style, "ascii" and "notty" included, leaves the "##" in the
// output. A learner reading "## 08:15, Monday" is reading markdown that failed
// to render, and the acceptance criterion asks for a briefing that renders.
func TestBriefingRendersHeadingMarkersAway(t *testing.T) {
	for _, color := range []bool{false, true} {
		out := stripANSI(renderMarkdown("## 08:15, Monday\n\nA paragraph.\n", 76, color))

		if strings.Contains(out, "#") {
			t.Errorf("color=%v: the heading marker survived rendering:\n%s", color, out)
		}
		if !strings.Contains(out, "08:15, Monday") {
			t.Errorf("color=%v: the heading text was lost:\n%s", color, out)
		}
	}
}

// TestBriefingDoesNotInflateWithPaddingEscapes pins the second reason.
//
// glamour's "dark" style paints every padding space to the wrap width
// individually, turning nav-01's 352-byte briefing into 5,698 bytes with 669
// escape sequences. That is invisible on a terminal but it matters here, because
// the `brief` control verb sends its reply back through the FIFO where replies
// are capped: a long briefing under that style could be truncated for no reason
// a learner could understand.
//
// The bound is deliberately loose. It is not trying to pin an exact byte count,
// which would break on any glamour patch release; it is catching a regression of
// an order of magnitude.
func TestBriefingDoesNotInflateWithPaddingEscapes(t *testing.T) {
	md := briefingLevel().Briefing

	coloured := renderMarkdown(md, 76, true)
	if len(coloured) > 4*len(md) {
		t.Errorf("rendering inflated %d bytes of markdown to %d, which suggests per-space padding escapes are back:\n%q",
			len(md), len(coloured), coloured)
	}

	escapes := strings.Count(coloured, "\x1b")
	if escapes > 40 {
		t.Errorf("rendering emitted %d escape sequences for %d bytes of markdown; a standard style emitted 669 for a comparable briefing",
			escapes, len(md))
	}
}

// TestBriefingHasNoTrailingWhitespace keeps a briefing readable in a diff.
//
// glamour pads every line out to the wrap width. On a terminal that is
// invisible, but a briefing also ends up in bug reports and CI transcripts,
// where columns of trailing spaces make a diff unreadable.
func TestBriefingHasNoTrailingWhitespace(t *testing.T) {
	var buf bytes.Buffer
	printBriefing(&buf, briefingLevel(), 76, false)

	for i, line := range strings.Split(buf.String(), "\n") {
		if line != strings.TrimRight(line, " \t") {
			t.Errorf("line %d has trailing whitespace: %q", i+1, line)
		}
	}
}

// TestClampBriefWidth bounds the width, because neither extreme is readable and
// a terminal can report either.
func TestClampBriefWidth(t *testing.T) {
	tests := []struct {
		in, want int
	}{
		{in: 0, want: minBriefWidth},
		{in: -1, want: minBriefWidth},
		{in: 10, want: minBriefWidth},
		{in: minBriefWidth, want: minBriefWidth},
		{in: 80, want: 80},
		{in: maxBriefWidth, want: maxBriefWidth},
		{in: 500, want: maxBriefWidth},
	}
	for _, tt := range tests {
		if got := clampBriefWidth(tt.in); got != tt.want {
			t.Errorf("clampBriefWidth(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// TestTerminalWidthFallsBackOffATerminal covers the CI and pipe case. A
// transcript still has to be readable.
func TestTerminalWidthFallsBackOffATerminal(t *testing.T) {
	if got := terminalWidth(&bytes.Buffer{}); got != defaultBriefWidth {
		t.Errorf("terminalWidth(buffer) = %d, want %d", got, defaultBriefWidth)
	}

	// A real *os.File that is not a terminal takes the same path, through
	// term.GetSize failing rather than through the type assertion.
	f, err := os.Create(filepath.Join(t.TempDir(), "not-a-terminal"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if got := terminalWidth(f); got != defaultBriefWidth {
		t.Errorf("terminalWidth(regular file) = %d, want %d", got, defaultBriefWidth)
	}
}

// TestBossLevelShowsNoNumberedSteps pins the presentation rule from
// docs/LEVEL-FORMAT.md section 2. A boss level's point is that the learner works
// out the shape themselves, so numbered steps would read as instructions.
//
// The objectives are still listed: they are what `check` verifies, and hiding
// them would make the level unfair rather than hard.
func TestBossLevelShowsNoNumberedSteps(t *testing.T) {
	level := briefingLevel()
	level.Boss = true

	var buf bytes.Buffer
	printBriefing(&buf, level, 80, false)
	out := buf.String()

	if regexp.MustCompile(`\d+\.\s+\[ \]`).MatchString(out) {
		t.Errorf("a boss level printed numbered steps:\n%s", out)
	}
	if !strings.Contains(out, "quest/answer.txt holds the folder you are standing in") {
		t.Errorf("a boss level hid its objectives, which makes it unfair rather than hard:\n%s", out)
	}

	// A non-boss level does number them, so the assertion above is meaningful.
	level.Boss = false
	var numbered bytes.Buffer
	printBriefing(&numbered, level, 80, false)
	if !regexp.MustCompile(`1\.\s+\[ \]`).MatchString(numbered.String()) {
		t.Errorf("a non-boss level printed no numbered steps, so the boss assertion proves nothing:\n%s", numbered.String())
	}
}

// TestBriefingWithNoObjectivesPrintsNoChecklist keeps a level with no objectives
// from printing an empty heading. The validator requires at least one, so this
// is defensive rather than expected.
func TestBriefingWithNoObjectivesPrintsNoChecklist(t *testing.T) {
	level := briefingLevel()
	level.Objectives = nil

	var buf bytes.Buffer
	printBriefing(&buf, level, 80, false)

	if strings.Contains(buf.String(), "What needs to be true") {
		t.Errorf("a level with no objectives printed a checklist heading:\n%s", buf.String())
	}
}

// TestGoDirectiveStaysAtTheSupportedFloor guards the go.mod floor against a
// dependency bump that moves it as a side effect.
//
// The floor is a deliberate decision, not a number the toolchain gets to pick.
// It changes the minimum Go somebody needs to build from source, and a `go get`
// will move it silently, in a file nobody rereads. This test exists so that
// moving it is always a commit somebody wrote a reason for.
//
// It sat at 1.23.0 until govulncheck reported GO-2026-5970 in golang.org/x/text,
// which charmbracelet/glamour brings in. The fix, x/text v0.39.0, declares
// `go 1.25.0`, and Go refuses to build a module whose dependency asks for a
// newer directive than its own, so the floor and the fix could not be had
// separately. The maintainer chose the library over the old floor: glamour is
// maintained upstream and will carry features this project has not specified
// yet, and a NEWER minimum Go is not a security cost. It ships users a standard
// library with more fixes in it, not fewer.
//
// So the number below is now 1.25.0, and the rule it enforces has not changed:
// if a dependency bump moves this line, find out why before accepting it.
func TestGoDirectiveStaysAtTheSupportedFloor(t *testing.T) {
	const wantFloor = "go 1.25.0"

	body, err := os.ReadFile(filepath.Join(cliModuleRoot(t), "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}

	var found string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "go ") && !strings.HasPrefix(line, "go1") {
			found = line
			break
		}
	}

	if found == "" {
		t.Fatal("go.mod has no go directive")
	}
	if found != wantFloor {
		t.Errorf("go.mod says %q, want %q.\n"+
			"A dependency bump has moved this module's minimum Go version. Check the new dependency's own "+
			"go directive. Pinning the dependency back is the default answer; raising the floor is a decision "+
			"that needs a reason written down, the way the x/text security bump has one. "+
			"See the comment above the require block in go.mod.", found, wantFloor)
	}
}
