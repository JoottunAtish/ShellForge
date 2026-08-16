package main

import (
	"context"
	"strings"
	"testing"
)

// TestGameResponderBriefIncludesTheChecklist is the regression test for the
// blocking review finding: `brief`, typed inside a live session, used to
// render only the briefing prose and drop the objective checklist entirely,
// even though the pre-attach screen shows both. The checklist is the only
// place a level's deliverable filenames and paths appear when the briefing
// prose itself does not name them, and `hint` sends a learner who asks for
// help back to `brief` for exactly that list.
//
// gameResponder.Reply's "brief" branch never touches r.session, so a nil
// session is fine here: this is the same seam TestEveryReplyLineEndsCRLF
// exercises for renderCheckReply, one level up.
func TestGameResponderBriefIncludesTheChecklist(t *testing.T) {
	level := briefingLevel()
	r := &gameResponder{level: level, color: false}

	out := r.Reply(context.Background(), "brief", "")

	for _, want := range []string{level.Objectives[0].Text, level.Objectives[1].Text, "(bonus)"} {
		if !strings.Contains(out, want) {
			t.Errorf("brief reply lost %q, so the checklist did not render:\n%q", want, out)
		}
	}

	// The briefing prose must still be there too: brief replaces nothing,
	// it adds the checklist underneath.
	if !strings.Contains(out, "08:15") {
		t.Errorf("brief reply lost the briefing prose:\n%q", out)
	}
}

// TestGameResponderBriefEndsCRLF belongs with TestEveryReplyLineEndsCRLF in
// render_check_test.go in spirit: gameResponder.Reply writes straight into a
// raw-mode terminal on four of its five branches, and only renderCheckReply
// and renderCheckError were covered by that table. A missing crlf() on any of
// the other branches is the same staircase bug and would ship the same way.
func TestGameResponderBriefEndsCRLF(t *testing.T) {
	level := briefingLevel()
	r := &gameResponder{level: level, color: false}

	out := r.Reply(context.Background(), "brief", "")

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
}

// TestGameResponderHintPointsAtBriefTruthfully pins the other half of the
// same review finding. The hint stub tells a learner to type `brief` to see
// the objectives again; now that brief actually shows them, this only
// documents the fix stays true rather than doing anything on its own.
func TestGameResponderHintPointsAtBriefTruthfully(t *testing.T) {
	level := briefingLevel()
	r := &gameResponder{level: level, color: false}

	hint := r.Reply(context.Background(), "hint", "")
	if !strings.Contains(hint, "brief") {
		t.Fatalf("the hint stub no longer points the learner at `brief`:\n%q", hint)
	}

	brief := r.Reply(context.Background(), "brief", "")
	if !strings.Contains(brief, level.Objectives[0].Text) {
		t.Errorf("hint points at `brief` for the objectives, but brief does not show them:\n%q", brief)
	}
}
