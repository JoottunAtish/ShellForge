package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// passingResult is a two-objective pass, one of them a bonus that was earned.
func passingResult() verify.LevelResult {
	return verify.LevelResult{
		LevelID: "nav-01",
		Passed:  true,
		Objectives: []verify.ObjectiveResult{
			{ID: "location", Text: "quest/answer.txt holds the folder you are standing in", Status: verify.StatusPass},
			{ID: "used-pwd", Text: "Found it with a single command", Status: verify.StatusPass, Optional: true},
		},
		Notes:      []string{},
		DurationMS: 412,
	}
}

// failingResult has two failing required objectives, a missed bonus, one pass,
// and a warn note. It is the shape that exercises every rule at once.
func failingResult() verify.LevelResult {
	first := verify.ObjectiveResult{
		ID: "obj2", Text: "summary.txt has one line per log file", Status: verify.StatusFail,
		Message: "FIRST: summary.txt has 3 lines but there are 4 log files.",
	}
	return verify.LevelResult{
		LevelID: "files-03",
		Passed:  false,
		Objectives: []verify.ObjectiveResult{
			{ID: "obj1", Text: "The reports directory exists", Status: verify.StatusPass},
			first,
			{ID: "obj3", Text: "summary.txt is sorted by size", Status: verify.StatusFail,
				Message: "SECOND: sort by size, largest first."},
			{ID: "obj4", Text: "summary.txt ends with a newline", Status: verify.StatusFail,
				Optional: true, Message: "BONUS: a trailing newline is a habit worth building."},
		},
		PrimaryFailure: &first,
		Notes: []string{
			"BONUS: a trailing newline is a habit worth building.",
			"WARN: archive/ is mode 0777. That works, and 0755 is the habit worth building.",
		},
		DurationMS: 1200,
	}
}

// TestRenderCheckReplyShowsOnlyTheFirstRequiredFailure is the Day 2 exit
// criterion "only the first failing required objective is shown", and the reason
// this renderer exists at all.
func TestRenderCheckReplyShowsOnlyTheFirstRequiredFailure(t *testing.T) {
	out := renderCheckReply(failingResult(), false)

	// The first required failure's message appears exactly once, as the body.
	if got := strings.Count(out, "FIRST:"); got != 1 {
		t.Errorf("the first required failure's on_fail appears %d times, want exactly 1:\n%s", got, out)
	}

	// The second required failure got its line but not its message.
	if strings.Contains(out, "SECOND:") {
		t.Errorf("a second required failure's on_fail was printed; only the first is shown:\n%s", out)
	}
	if !strings.Contains(out, "summary.txt is sorted by size") {
		t.Errorf("the second failing objective has no checklist line at all:\n%s", out)
	}
}

// TestRenderCheckReplyMarksEveryObjective asserts the checklist is complete and
// each mark means what it should. A learner reads the marks before the prose.
func TestRenderCheckReplyMarksEveryObjective(t *testing.T) {
	out := renderCheckReply(failingResult(), false)

	tests := []struct {
		mark, text string
	}{
		{markPass, "The reports directory exists"},
		{markFail, "summary.txt has one line per log file"},
		{markFail, "summary.txt is sorted by size"},
		{markBonus, "summary.txt ends with a newline"},
	}
	for _, tt := range tests {
		line := findLine(out, tt.text)
		if line == "" {
			t.Errorf("no line for objective %q", tt.text)
			continue
		}
		if !strings.Contains(line, tt.mark) {
			t.Errorf("objective %q is marked %q, want %q", tt.text, line, tt.mark)
		}
	}
}

// TestRenderCheckReplyMarksABonusAsBonus keeps a missed bonus from reading like
// a failure. A learner who passed must not be shown a red cross.
func TestRenderCheckReplyMarksABonusAsBonus(t *testing.T) {
	out := renderCheckReply(failingResult(), false)

	line := findLine(out, "summary.txt ends with a newline")
	if !strings.Contains(line, markBonus) {
		t.Errorf("a missed bonus is marked %q, want %q", line, markBonus)
	}
	if strings.Contains(line, markFail) {
		t.Errorf("a missed bonus is marked as a failure: %q", line)
	}
	if !strings.Contains(line, "(bonus)") {
		t.Errorf("a bonus objective is not labelled as one: %q", line)
	}
}

// TestRenderCheckReplyPutsNotesBelowTheFailure pins the ordering. Notes are
// advice; the thing the learner has to fix comes first.
func TestRenderCheckReplyPutsNotesBelowTheFailure(t *testing.T) {
	out := renderCheckReply(failingResult(), false)

	failureAt := strings.Index(out, "FIRST:")
	notesAt := strings.Index(out, "Notes:")
	warnAt := strings.Index(out, "WARN:")

	if failureAt < 0 || notesAt < 0 || warnAt < 0 {
		t.Fatalf("missing a section:\n%s", out)
	}
	if notesAt < failureAt {
		t.Error("notes were printed above the failure the learner has to fix")
	}
	if warnAt < notesAt {
		t.Error("a note appeared before the Notes heading")
	}
}

// TestRenderCheckReplyOnAPass covers the output a learner sees most often.
func TestRenderCheckReplyOnAPass(t *testing.T) {
	out := renderCheckReply(passingResult(), false)

	if !strings.Contains(out, "PASS") {
		t.Errorf("a passing result does not say PASS:\n%s", out)
	}
	if strings.Contains(out, markFail) {
		t.Errorf("a passing result contains a failure mark:\n%s", out)
	}
	if strings.Contains(out, "Notes:") {
		t.Errorf("a passing result with no notes printed a Notes heading:\n%s", out)
	}
	if !strings.Contains(out, "1 of 1 bonus") {
		t.Errorf("the earned bonus is not reported:\n%s", out)
	}
}

// TestRenderCheckReplyDistinguishesTimeoutFromFailure is acceptance criterion 4
// at the point the learner actually reads it. A timeout must never look like a
// wrong answer, because the remediation is completely different.
func TestRenderCheckReplyDistinguishesTimeoutFromFailure(t *testing.T) {
	timedOut := verify.ObjectiveResult{
		ID: "obj1", Text: "Every log file is copied into archive/", Status: verify.StatusTimeout,
		Message: "this check did not finish within 10s, so it was stopped.",
	}
	res := verify.LevelResult{
		LevelID:        "files-03",
		Objectives:     []verify.ObjectiveResult{timedOut},
		PrimaryFailure: &timedOut,
		Notes:          []string{},
	}

	out := renderCheckReply(res, false)

	if !strings.Contains(out, markUnknown) {
		t.Errorf("a timeout is not marked %q:\n%s", markUnknown, out)
	}
	if strings.Contains(out, markFail) {
		t.Errorf("a timeout is marked as a failure:\n%s", out)
	}
	if !strings.Contains(out, "not a wrong answer") {
		t.Errorf("the summary does not tell the learner a timeout is not a wrong answer:\n%s", out)
	}
	if !strings.Contains(out, "timed out") {
		t.Errorf("the objective line does not say it timed out:\n%s", out)
	}
}

// TestRenderCheckReplyDistinguishesErrorFromFailure is the same for a check that
// could not determine an answer at all.
func TestRenderCheckReplyDistinguishesErrorFromFailure(t *testing.T) {
	errored := verify.ObjectiveResult{
		ID: "obj1", Text: "ATLAS_ENV is set to production", Status: verify.StatusError,
		Message: "the shell has not reported its state yet",
	}
	res := verify.LevelResult{
		Objectives:     []verify.ObjectiveResult{errored},
		PrimaryFailure: &errored,
		Notes:          []string{},
	}

	out := renderCheckReply(res, false)

	if !strings.Contains(out, markUnknown) {
		t.Errorf("an error is not marked %q:\n%s", markUnknown, out)
	}
	if !strings.Contains(out, "not a wrong answer") {
		t.Errorf("the summary does not distinguish an error from a failure:\n%s", out)
	}
	if !strings.Contains(out, "could not be checked") {
		t.Errorf("the objective line does not say the check could not run:\n%s", out)
	}
}

// TestEveryReplyLineEndsCRLF is the raw-mode rule. Without it the learner's
// terminal renders the reply as a staircase, which looks like the game is
// broken.
func TestEveryReplyLineEndsCRLF(t *testing.T) {
	replies := map[string]string{
		"pass":    renderCheckReply(passingResult(), false),
		"fail":    renderCheckReply(failingResult(), false),
		"colour":  renderCheckReply(failingResult(), true),
		"error":   renderCheckError(errors.New("container is not running"), "nav-01"),
		"empty":   renderCheckReply(verify.LevelResult{Objectives: []verify.ObjectiveResult{}, Notes: []string{}}, false),
		"multiln": renderCheckReply(multilineMessageResult(), false),
	}

	for name, out := range replies {
		t.Run(name, func(t *testing.T) {
			for i := 0; i < len(out); i++ {
				if out[i] != '\n' {
					continue
				}
				if i == 0 || out[i-1] != '\r' {
					t.Fatalf("a newline at byte %d is not preceded by a carriage return; raw mode would render this as a staircase:\n%q", i, out)
				}
			}
			if !strings.HasSuffix(out, "\r\n") {
				t.Errorf("the reply does not end in CRLF; the shim prints it verbatim:\n%q", out)
			}
			if strings.Contains(out, "\r\r") {
				t.Errorf("a doubled carriage return would render as a stray blank column:\n%q", out)
			}
		})
	}
}

// multilineMessageResult carries an authored on_fail spanning several lines,
// which is the normal shape of a YAML block scalar.
func multilineMessageResult() verify.LevelResult {
	failure := verify.ObjectiveResult{
		ID: "obj1", Text: "an objective", Status: verify.StatusFail,
		Message: "first line of the authored message\nsecond line\nthird line",
	}
	return verify.LevelResult{
		Objectives:     []verify.ObjectiveResult{failure},
		PrimaryFailure: &failure,
		Notes:          []string{},
	}
}

// TestCRLFIsIdempotentAndSurvivesAuthoredCarriageReturns covers the case a
// Windows level author produces. .gitattributes enforces LF in the repository,
// but a pack loaded from disk elsewhere can still carry CRLF, and a naive
// implementation would turn that into "\r\r\n".
func TestCRLFIsIdempotentAndSurvivesAuthoredCarriageReturns(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "a\nb\n", want: "a\r\nb\r\n"},
		{name: "already crlf", in: "a\r\nb\r\n", want: "a\r\nb\r\n"},
		{name: "mixed", in: "a\r\nb\n", want: "a\r\nb\r\n"},
		{name: "empty", in: "", want: ""},
		{name: "no trailing newline", in: "a", want: "a"},
		{name: "leading newline", in: "\na", want: "\r\na"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := crlf(tt.in); got != tt.want {
				t.Errorf("crlf(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if double := crlf(crlf(tt.in)); double != tt.want {
				t.Errorf("crlf is not idempotent: crlf(crlf(%q)) = %q, want %q", tt.in, double, tt.want)
			}
		})
	}
}

// TestAuthoredCarriageReturnsDoNotDoubleUp is the same trap through the real
// renderer rather than through crlf directly.
func TestAuthoredCarriageReturnsDoNotDoubleUp(t *testing.T) {
	failure := verify.ObjectiveResult{
		ID: "obj1", Text: "an objective", Status: verify.StatusFail,
		Message: "a CRLF authored message\r\nsecond line\r\n",
	}
	res := verify.LevelResult{
		Objectives:     []verify.ObjectiveResult{failure},
		PrimaryFailure: &failure,
		Notes:          []string{},
	}

	out := renderCheckReply(res, false)
	if strings.Contains(out, "\r\r") {
		t.Errorf("a CRLF-authored on_fail produced a doubled carriage return:\n%q", out)
	}
	if !strings.Contains(out, "second line") {
		t.Errorf("the second line of the authored message was lost:\n%q", out)
	}
}

// TestColourIsOnlyEmittedWhenAsked is the NO_COLOR contract at this boundary.
// The decision is made once per session by ux.ColorEnabled and passed in.
func TestColourIsOnlyEmittedWhenAsked(t *testing.T) {
	plain := renderCheckReply(failingResult(), false)
	if strings.Contains(plain, "\x1b[") {
		t.Errorf("escape codes leaked into an uncoloured reply:\n%q", plain)
	}

	coloured := renderCheckReply(failingResult(), true)
	if !strings.Contains(coloured, "\x1b[") {
		t.Error("no escape codes in a coloured reply")
	}

	// Colour must not change the words, only their presentation. Stripping the
	// codes has to give back the plain reply exactly.
	if stripANSI(coloured) != plain {
		t.Errorf("colour changed the text, not just its presentation:\nplain:\n%q\nstripped:\n%q", plain, stripANSI(coloured))
	}
}

// TestReplyIsTruncatedRatherThanFloodingTheTerminal bounds a pathological level.
// The script check appends a failing script's whole stdout to its on_fail, so
// this is reachable from a level, not only from a bug.
func TestReplyIsTruncatedRatherThanFloodingTheTerminal(t *testing.T) {
	failure := verify.ObjectiveResult{
		ID: "obj1", Text: "an objective", Status: verify.StatusFail,
		Message: strings.Repeat("a very long line of diagnostic output\n", 2000),
	}
	res := verify.LevelResult{
		Objectives:     []verify.ObjectiveResult{failure},
		PrimaryFailure: &failure,
		Notes:          []string{},
	}

	out := renderCheckReply(res, false)

	// The CRLF pass adds a byte per line after truncation, so the bound is
	// checked against the pre-CRLF cap with room for that expansion.
	if len(out) > 2*maxReplyBytes {
		t.Errorf("reply is %d bytes, which is not bounded near maxReplyBytes (%d)", len(out), maxReplyBytes)
	}
	if !strings.Contains(out, truncationNotice) {
		t.Error("a truncated reply does not say it was truncated, so the learner would think that was all of it")
	}
	if !strings.HasSuffix(out, "\r\n") {
		t.Errorf("a truncated reply does not end in CRLF: %q", out[len(out)-10:])
	}
}

// TestRenderCheckErrorTellsTheLearnerTheirWorkIsSafe covers the could-not-run
// path. Somebody mid-level reading an internal error wonders about their files
// first.
func TestRenderCheckErrorTellsTheLearnerTheirWorkIsSafe(t *testing.T) {
	out := renderCheckError(errors.New("container is not running"), "files-03")

	for _, want := range []string{"could not run", "container is not running", "files are safe", "shellforge run files-03"} {
		if !strings.Contains(out, want) {
			t.Errorf("the message does not contain %q:\n%s", want, out)
		}
	}
}

// TestRenderCheckReplyOnAnEmptyResultDoesNotPanic is the degenerate case. A
// cancelled run before the first check produces no objectives, and the reply
// still has to be something.
func TestRenderCheckReplyOnAnEmptyResultDoesNotPanic(t *testing.T) {
	out := renderCheckReply(verify.LevelResult{
		Objectives: []verify.ObjectiveResult{},
		Notes:      []string{},
	}, false)

	if out == "" {
		t.Error("an empty result rendered as an empty reply, so the learner would see nothing at all")
	}
	if !strings.HasSuffix(out, "\r\n") {
		t.Errorf("reply does not end in CRLF: %q", out)
	}
}

// TestObjectiveWithNoTextFallsBackToItsID keeps a level that omitted objective
// text from rendering a bare mark on an empty line, which reads as a rendering
// bug rather than as a level missing a field.
func TestObjectiveWithNoTextFallsBackToItsID(t *testing.T) {
	res := verify.LevelResult{
		Objectives: []verify.ObjectiveResult{{ID: "nocheat", Status: verify.StatusPass}},
		Notes:      []string{},
	}
	out := renderCheckReply(res, false)
	if !strings.Contains(out, "nocheat") {
		t.Errorf("an objective with no text did not fall back to its id:\n%s", out)
	}
}

// findLine returns the first line containing want, or "".
func findLine(out, want string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, want) {
			return strings.TrimSuffix(line, "\r")
		}
	}
	return ""
}

// stripANSI removes every escape sequence, so a test can compare coloured and
// plain output for equality of words.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
