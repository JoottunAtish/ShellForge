package verify

import (
	"context"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/verify/verifytest"
)

func init() {
	markCovered("command_matched", "command_not_matched")
}

// fakeJournal is a minimal JournalReader for testing the journal checks. It
// ignores Scope.Kind and Scope.N beyond recording the last Scope it was
// asked for, and always answers with commands.
type fakeJournal struct {
	commands  []string
	lastScope Scope
}

func (f *fakeJournal) Commands(scope Scope) []string {
	f.lastScope = scope
	return f.commands
}

func TestCommandMatchedCheck(t *testing.T) {
	t.Run("pass: a command in scope matches", func(t *testing.T) {
		c := mustFactory(t, "command_matched", Spec{ID: "o", OnFail: "no pipeline seen", Params: map[string]any{"pattern": `grep.*\|\s*wc\s+-l`}})
		j := &fakeJournal{commands: []string{"ls -l", "grep ERROR log.txt | wc -l"}}
		r := c.Run(context.Background(), Env{Journal: j})
		if r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: no command matches", func(t *testing.T) {
		c := mustFactory(t, "command_matched", Spec{ID: "o", OnFail: "no pipeline seen", Params: map[string]any{"pattern": `grep.*\|\s*wc\s+-l`}})
		j := &fakeJournal{commands: []string{"ls -l", "cat log.txt"}}
		r := c.Run(context.Background(), Env{Journal: j})
		if r.Status != StatusFail || r.Message != "no pipeline seen" {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("scope: level is the default when scope is omitted", func(t *testing.T) {
		c := mustFactory(t, "command_matched", Spec{ID: "o", OnFail: "x", Params: map[string]any{"pattern": "x"}})
		j := &fakeJournal{commands: nil}
		c.Run(context.Background(), Env{Journal: j})
		if j.lastScope.Kind != ScopeLevel {
			t.Fatalf("got scope %+v, want ScopeLevel", j.lastScope)
		}
	})

	t.Run("scope: last", func(t *testing.T) {
		c := mustFactory(t, "command_matched", Spec{ID: "o", OnFail: "x", Params: map[string]any{"pattern": "x", "scope": "last"}})
		j := &fakeJournal{commands: nil}
		c.Run(context.Background(), Env{Journal: j})
		if j.lastScope.Kind != ScopeLast {
			t.Fatalf("got scope %+v, want ScopeLast", j.lastScope)
		}
	})

	t.Run("scope: last_n:5", func(t *testing.T) {
		c := mustFactory(t, "command_matched", Spec{ID: "o", OnFail: "x", Params: map[string]any{"pattern": "x", "scope": "last_n:5"}})
		j := &fakeJournal{commands: nil}
		c.Run(context.Background(), Env{Journal: j})
		if j.lastScope.Kind != ScopeLastN || j.lastScope.N != 5 {
			t.Fatalf("got scope %+v, want {ScopeLastN 5}", j.lastScope)
		}
	})

	t.Run("factory: invalid scope string", func(t *testing.T) {
		if _, err := newCommandMatchedCheck(Spec{ID: "o", Params: map[string]any{"pattern": "x", "scope": "sometimes"}}); err == nil {
			t.Fatal("expected an error for an invalid scope string")
		}
	})

	t.Run("factory: last_n without a number", func(t *testing.T) {
		if _, err := newCommandMatchedCheck(Spec{ID: "o", Params: map[string]any{"pattern": "x", "scope": "last_n:"}}); err == nil {
			t.Fatal("expected an error for last_n with no integer")
		}
	})

	t.Run("factory: invalid regex", func(t *testing.T) {
		if _, err := newCommandMatchedCheck(Spec{ID: "o", Params: map[string]any{"pattern": "("}}); err == nil {
			t.Fatal("expected an error for an invalid regex")
		}
	})
}

func TestCommandNotMatchedCheck(t *testing.T) {
	t.Run("pass: nothing matches the banned pattern", func(t *testing.T) {
		c := mustFactory(t, "command_not_matched", Spec{ID: "o", OnFail: "hardcoded literal used", Params: map[string]any{"pattern": `^\s*echo\s+\d+`}})
		j := &fakeJournal{commands: []string{"cat report.txt | wc -l"}}
		r := c.Run(context.Background(), Env{Journal: j})
		if r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: the banned pattern was used", func(t *testing.T) {
		c := mustFactory(t, "command_not_matched", Spec{ID: "o", OnFail: "hardcoded literal used", Params: map[string]any{"pattern": `^\s*echo\s+\d+`}})
		j := &fakeJournal{commands: []string{"echo 147 > report.txt"}}
		r := c.Run(context.Background(), Env{Journal: j})
		if r.Status != StatusFail || r.Message != "hardcoded literal used" {
			t.Fatalf("got %+v", r)
		}
	})
}

// TestForgedJournalLineDoesNotPassALevel is a REGRESSION PIN, not a
// red-to-green test: the guarantee it asserts already holds on main, because
// nothing in this package lets a journal check gate a level and nothing
// outside it reads the journal for a pass verdict. Stated plainly rather
// than hidden, because the testing skill's rule that a test which passes
// before and after tests nothing does not apply to a pin the ticket itself
// names as an acceptance criterion: its value is that it now fails if a
// later change lets a journal signal reach a pass verdict on its own, or lets
// a subset run stand in for a whole level.
//
// The forgery: a fakeJournal reporting "pwd" ran, exactly the line a learner
// could produce from inside the sandbox with a printf of the right OSC 133
// marker and a journal.tsv line by hand. The state check below never saw the
// file the level actually cares about.
func TestForgedJournalLineDoesNotPassALevel(t *testing.T) {
	specs := []Spec{
		{
			ID: "location", Type: "file_exists", OnFail: "answer.txt is missing",
			Params: map[string]any{"path": "/home/learner/quest/answer.txt"},
		},
		{
			ID: "used-pwd", Type: "command_matched", OnFail: "no shorter way seen", Optional: true,
			Params: map[string]any{"pattern": `^\s*pwd\s*$`},
		},
	}

	engine := NewEngine()
	checks, err := engine.Build(specs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	forged := &fakeJournal{commands: []string{"pwd"}}
	res := engine.Run(context.Background(), checks, Env{
		Session: verifytest.NewSession(verifytest.Const(
			runtime.ExecResult{ExitCode: 1, Stderr: []byte("stat: cannot statx 'answer.txt': No such file or directory")},
			nil)),
		Journal: forged,
	})

	if res.Passed {
		t.Fatal("a forged journal line let the level pass, even though the required state check failed")
	}

	var stateStatus, journalStatus Status
	for _, obj := range res.Objectives {
		switch obj.ID {
		case "location":
			stateStatus = obj.Status
		case "used-pwd":
			journalStatus = obj.Status
		}
	}

	// Asserting all four is what distinguishes "the forgery was ignored"
	// from "the forgery worked but something else failed too": the journal
	// objective genuinely reads the forged line as a match, the state
	// objective genuinely fails on real sandbox state, and PrimaryFailure
	// names the state objective, never the journal one.
	if journalStatus != StatusPass {
		t.Errorf("the journal objective is %s, want StatusPass: the forged line should read as a match", journalStatus)
	}
	if stateStatus != StatusFail {
		t.Errorf("the state objective is %s, want StatusFail: the sandbox never held the file", stateStatus)
	}
	if res.PrimaryFailure == nil || res.PrimaryFailure.ID != "location" {
		t.Fatalf("PrimaryFailure = %+v, want the state objective, not the journal one", res.PrimaryFailure)
	}
}
