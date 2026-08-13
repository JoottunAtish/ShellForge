package verify

import (
	"context"
	"testing"
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
