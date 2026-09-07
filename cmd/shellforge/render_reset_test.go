package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/content/setup"
)

// fakeResetter records whether a rebuild was actually asked for, which is
// the property every refusal below has to hold.
type fakeResetter struct {
	calls int
	err   error
}

func (f *fakeResetter) Reset(context.Context) error {
	f.calls++
	return f.err
}

func resetReply(t *testing.T, r *fakeResetter, args string) string {
	t.Helper()
	return renderResetReply(context.Background(), r, "/home/learner/quest", args)
}

func TestResetWithNoFlagsExplainsAndDeletesNothing(t *testing.T) {
	r := &fakeResetter{}
	got := resetReply(t, r, "")

	if r.calls != 0 {
		t.Errorf("a bare `reset` rebuilt the level %d times, want 0", r.calls)
	}
	if !strings.Contains(got, "/home/learner/quest") {
		t.Errorf("reply does not name the level root:\n%s", got)
	}
	if !strings.Contains(got, "Files elsewhere in your sandbox are untouched") {
		t.Errorf("reply does not say what is NOT deleted, which is what makes it safe to type:\n%s", got)
	}
	if !strings.Contains(got, "reset --yes") {
		t.Errorf("reply does not name the command that confirms it:\n%s", got)
	}
}

func TestResetYesRebuildsAndSaysSo(t *testing.T) {
	r := &fakeResetter{}
	got := resetReply(t, r, "--yes")

	if r.calls != 1 {
		t.Errorf("`reset --yes` rebuilt %d times, want 1", r.calls)
	}
	if !strings.Contains(got, "Level rebuilt") {
		t.Errorf("reply does not confirm the rebuild:\n%s", got)
	}
	if !strings.Contains(got, "brief") {
		t.Errorf("reply does not say what to do next:\n%s", got)
	}
}

func TestAnUnknownResetFlagIsRefusedByNameAndDeletesNothing(t *testing.T) {
	r := &fakeResetter{}
	got := resetReply(t, r, "--force")

	if r.calls != 0 {
		t.Error("an unknown flag reached the rebuild")
	}
	if !strings.Contains(got, `"--force"`) {
		t.Errorf("reply does not name the flag it refused:\n%s", got)
	}
	if !strings.Contains(got, "nothing was deleted") {
		t.Errorf("reply does not say nothing was deleted:\n%s", got)
	}
}

// An unsafe level root is a content bug the learner can neither cause nor
// fix, and the message says so rather than blaming them.
func TestAnUnsafeLevelRootIsReportedAsAContentBug(t *testing.T) {
	r := &fakeResetter{err: fmt.Errorf("refusing to use %q as a level root: %w", "/", setup.ErrUnsafeLevelRoot)}
	got := resetReply(t, r, "--yes")

	if !strings.Contains(got, "deleted nothing") {
		t.Errorf("reply does not say nothing was deleted:\n%s", got)
	}
	if !strings.Contains(got, "problem with the level rather than with anything you did") {
		t.Errorf("reply blames the learner for a content bug:\n%s", got)
	}
	if !strings.Contains(got, "bug-report") {
		t.Errorf("reply does not say what to do about it:\n%s", got)
	}
}

func TestAFailedRebuildSaysHowToRecover(t *testing.T) {
	r := &fakeResetter{err: errors.New("the sandbox went away")}
	got := resetReply(t, r, "--yes")

	if !strings.Contains(got, "could not finish") {
		t.Errorf("reply does not say the rebuild failed:\n%s", got)
	}
	if !strings.Contains(got, "exit") {
		t.Errorf("reply does not name the next command to run:\n%s", got)
	}
}

func TestResetRepliesHaveNoEscapeSequence(t *testing.T) {
	for _, args := range []string{"", "--yes", "--nonsense"} {
		got := resetReply(t, &fakeResetter{}, args)
		if strings.Contains(got, "\x1b[") {
			t.Errorf("%q: reply contains an escape sequence:\n%q", args, got)
		}
	}
}

func TestResetRepliesAreCRLFTerminatedThroughTheResponder(t *testing.T) {
	got := crlf(resetReply(t, &fakeResetter{}, ""))
	for i, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if line != "" && !strings.HasSuffix(line, "\r") {
			t.Errorf("line %d does not end in a carriage return: %q", i, line)
		}
	}
}
