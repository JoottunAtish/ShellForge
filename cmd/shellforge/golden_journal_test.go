package main

import (
	"context"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/verify"
	"github.com/JoottunAtish/ShellForge/internal/verify/verifytest"
)

// Pure Go coverage for the golden harness's journal, needing no Docker
// daemon. See docs/LEVEL-FORMAT.md section 7's new subsection for what this
// models and what it deliberately does not.

// TestSolutionJournalHonoursEveryScopeKind pins the three verify.ScopeKind
// answers against a fixed command list, and that Commands before Record
// returns nothing, which is the state a fresh sandbox really has during the
// golden harness's pre-check phase.
func TestSolutionJournalHonoursEveryScopeKind(t *testing.T) {
	j := newSolutionJournal()

	if got := j.Commands(verify.Scope{Kind: verify.ScopeLevel}); len(got) != 0 {
		t.Errorf("Commands before Record = %v, want none", got)
	}

	j.Record([]string{"pwd", "ls -la", "cat report.txt"})

	tests := []struct {
		name  string
		scope verify.Scope
		want  []string
	}{
		{"level: every command in order", verify.Scope{Kind: verify.ScopeLevel}, []string{"pwd", "ls -la", "cat report.txt"}},
		{"last: only the final command", verify.Scope{Kind: verify.ScopeLast}, []string{"cat report.txt"}},
		{"last_n smaller than the count", verify.Scope{Kind: verify.ScopeLastN, N: 2}, []string{"ls -la", "cat report.txt"}},
		{"last_n equal to the count", verify.Scope{Kind: verify.ScopeLastN, N: 3}, []string{"pwd", "ls -la", "cat report.txt"}},
		{"last_n larger than the count", verify.Scope{Kind: verify.ScopeLastN, N: 10}, []string{"pwd", "ls -la", "cat report.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := j.Commands(tt.scope)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("Commands(%+v) = %v, want %v", tt.scope, got, tt.want)
			}
		})
	}
}

// TestSolutionJournalCommandsTreatsAnUnknownScopeAsNoCommands pins the
// default branch's direction: a ScopeKind Commands does not recognize
// answers with no commands, not with the whole history. The registered
// three all have their own case above this one, so reaching the default
// branch at all means a fifteenth ScopeKind was added with nothing here
// updated for it, and under-reporting is the safe way for that gap to fail:
// a journal check can only grant a bonus, never gate a level, so withholding
// one is a nicety lost, never a wrong answer that pays out.
func TestSolutionJournalCommandsTreatsAnUnknownScopeAsNoCommands(t *testing.T) {
	j := newSolutionJournal()
	j.Record([]string{"pwd", "ls -la", "cat report.txt"})

	if got := j.Commands(verify.Scope{Kind: verify.ScopeKind("future_kind")}); got != nil {
		t.Errorf("Commands with an unrecognized ScopeKind = %v, want nil", got)
	}
}

// TestSolutionCommandsSkipsBlankAndCommentLines is the split solutionCommands
// does on a level's authored solution text: one command per non-empty,
// non-comment line, in order.
func TestSolutionCommandsSkipsBlankAndCommentLines(t *testing.T) {
	got := solutionCommands("pwd\n\n# a comment\nls -la\n   \n\t# indented comment\ncat report.txt\n")
	want := []string{"pwd", "ls -la", "cat report.txt"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("solutionCommands = %v, want %v", got, want)
	}
}

// isJournalOnlyOptionalObjective reports whether obj is an optional objective
// whose check tree, found by id in checks, names only journal check types.
func isJournalOnlyOptionalObjective(obj content.Objective, checks []content.CheckSpec) (*content.CheckSpec, bool) {
	if !obj.Optional {
		return nil, false
	}
	for i := range checks {
		if checks[i].ID == obj.ID {
			return &checks[i], isJournalOnlyCheck(&checks[i])
		}
	}
	return nil, false
}

// specFromCheck converts one content.CheckSpec into a verify.Spec, field for
// field, the same mirroring internal/game's own unexported verifySpec relies
// on. It is deliberately narrow, existing only to hand the engine one spec
// for a check this file already knows is a journal check: it carries no
// depth bound and no ambiguous-shape refusal, both of which verifySpec has
// and a hand-authored, already-validated shipped pack has no need of here.
func specFromCheck(c *content.CheckSpec, text string) verify.Spec {
	spec := verify.Spec{
		ID:             c.ID,
		Type:           c.Type,
		OnFail:         c.OnFail,
		Severity:       c.Severity,
		TimeoutSeconds: c.TimeoutSeconds,
		Params:         c.Params,
		Text:           text,
	}
	for i := range c.AnyOf {
		spec.AnyOf = append(spec.AnyOf, specFromCheck(&c.AnyOf[i], ""))
	}
	for i := range c.AllOf {
		spec.AllOf = append(spec.AllOf, specFromCheck(&c.AllOf[i], ""))
	}
	if c.Not != nil {
		branch := specFromCheck(c.Not, "")
		spec.Not = &branch
	}
	return spec
}

// TestEveryShippedJournalObjectivePassesItsOwnSolution is the pin RECON
// measured directly: every optional objective in the shipped pack whose
// check tree is entirely journal check types passes against a journal built
// from that level's own solution. It runs under plain `go test ./...`, no
// Docker, and is a PIN rather than a red-to-green test: the invariant it
// locks in already holds on main, verified by a throwaway probe during
// reconnaissance. It is written now so a later change that breaks the
// composition (the harness journal, isJournalOnlyCheck, or a level edit)
// fails here instead of only in the Sandbox image CI job.
//
// A built Check is run directly, not through Session.Check, deliberately:
// three of the shipped journal-only objectives (no-flooding, and the
// severity: warn checks with no objective of their own) never produce a
// verify.ObjectiveResult at all, because the engine's own append folds a
// severity: warn outcome into a Note instead. Calling Check.Run bypasses
// that reporting layer and asks the only question this pin cares about: did
// the check itself read the modelled journal as a pass.
func TestEveryShippedJournalObjectivePassesItsOwnSolution(t *testing.T) {
	pack, err := content.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}

	tested := 0
	for i := range pack.Levels {
		level := &pack.Levels[i]
		for _, obj := range level.Objectives {
			c, ok := isJournalOnlyOptionalObjective(obj, level.Checks)
			if !ok {
				continue
			}

			t.Run(level.ID+"/"+obj.ID, func(t *testing.T) {
				sj := newSolutionJournal()
				sj.Record(solutionCommands(level.Solution))

				checks, err := verify.NewEngine().Build([]verify.Spec{specFromCheck(c, obj.Text)})
				if err != nil {
					t.Fatalf("Build: %v", err)
				}

				result := checks[0].Run(context.Background(), verify.Env{
					Journal: sj,
					Session: verifytest.NewSession(),
					LevelID: level.ID,
				})
				if result.Status != verify.StatusPass {
					t.Errorf("check %q is %s after the level's own solution, want StatusPass: %s",
						obj.ID, result.Status, result.Message)
				}
			})
			tested++
		}
	}

	// The count is logged, not asserted: a hard number fails the build the
	// day somebody adds a legitimate 26th level, and the per-objective
	// assertion above is the content that matters. It must never be zero,
	// which is the vacuous-pass this test exists to rule out.
	t.Logf("%d shipped journal-only optional objectives exercised", tested)
	if tested == 0 {
		t.Fatal("no journal-only optional objective was found in the shipped pack; this test would otherwise pass vacuously")
	}
}
