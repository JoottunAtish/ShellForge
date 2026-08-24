package journal_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/journal"
	"github.com/JoottunAtish/ShellForge/internal/scope"
	"github.com/JoottunAtish/ShellForge/internal/store"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// *Journal satisfies verify.JournalReader directly, with no adapter. Commands
// takes scope.Scope and verify.Scope is a type ALIAS for scope.Scope, so the
// two are one type rather than two that happen to match field for field.
//
// This assertion has to live in a _test.go file, and that is not a shortcut.
// Naming the identifier verify.JournalReader requires importing
// internal/verify from the file that names it, and internal/journal is layer
// 2 while internal/verify is layer 3: a non-test file here would be exactly
// the upward edge internal/archtest forbids and internal/verify/doc.go rules
// out in the other direction. archtest's collectImports skips _test.go files,
// so an external test package may name both sides and prove they meet.
var _ verify.JournalReader = (*journal.Journal)(nil)

// aliasProof compiles only if verify.Scope and scope.Scope are the same type.
// Go assignability needs identical underlying types AND at least one side
// unnamed, so two separately declared named structs would fail here even with
// identical fields. Changing verify.Scope from an alias to a named type breaks
// this line at compile time, before any test runs.
var aliasProof scope.Scope = verify.Scope{Kind: verify.ScopeLevel}

func openTestJournal(t *testing.T) *journal.Journal {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "progress.db")
	s, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return journal.New(s)
}

// TestVerifyScopeAliasesScope is the runtime half of the compile-time proof
// above. reflect reports one type for both names only when verify.Scope is an
// alias; a named type would report its own name and package path here.
func TestVerifyScopeAliasesScope(t *testing.T) {
	_ = aliasProof

	if got, want := reflect.TypeOf(verify.Scope{}), reflect.TypeOf(scope.Scope{}); got != want {
		t.Errorf("verify.Scope is %s.%s, scope.Scope is %s.%s; verify.Scope must be a type alias, not a named type",
			got.PkgPath(), got.Name(), want.PkgPath(), want.Name())
	}
	if got, want := reflect.TypeOf(verify.ScopeKind("")), reflect.TypeOf(scope.ScopeKind("")); got != want {
		t.Errorf("verify.ScopeKind is %s.%s, scope.ScopeKind is %s.%s; verify.ScopeKind must be a type alias, not a named type",
			got.PkgPath(), got.Name(), want.PkgPath(), want.Name())
	}
}

// TestScopeKindStringValues pins the wire values. These three strings are the
// level YAML surface: a journal check writes scope: level, scope: last, or
// scope: last_n:N, and internal/verify's parseScope turns that text into one
// of these constants. Renaming one here would break every level that uses a
// journal check, silently, because a kind nothing produces simply never
// matches.
func TestScopeKindStringValues(t *testing.T) {
	cases := []struct {
		name string
		kind scope.ScopeKind
		want string
	}{
		{"Level", scope.Level, "level"},
		{"LastN", scope.LastN, "last_n"},
		{"Last", scope.Last, "last"},
	}

	// wantKinds pins the set SIZE, not only the members listed above. Adding
	// a kind means adding the arm that answers it in journal's commands and
	// the text that produces it in verify's parseScope; a kind with no arm
	// falls through to journal's default and becomes an error no level author
	// asked for.
	const wantKinds = 3
	if len(cases) != wantKinds {
		t.Fatalf("this test enumerates %d scope kinds, want %d; a kind was added to internal/scope without updating this table", len(cases), wantKinds)
	}

	for _, c := range cases {
		if string(c.kind) != c.want {
			t.Errorf("scope.%s = %q, want %q", c.name, string(c.kind), c.want)
		}
	}

	// The aliases carry the same values, which is what lets a level's YAML
	// text reach journal's switch unchanged.
	if verify.ScopeLevel != scope.Level || verify.ScopeLastN != scope.LastN || verify.ScopeLast != scope.Last {
		t.Error("verify's scope constants no longer equal internal/scope's; they must be aliases, not copies")
	}
}

// TestJournalAnswersThroughVerifyJournalReader drives a real Journal through
// the interface verification uses, for every scope kind. There is no adapter
// and no translation left to test: the point is that the same values reach
// the same query whether the caller spells the type verify.Scope or
// scope.Scope.
func TestJournalAnswersThroughVerifyJournalReader(t *testing.T) {
	j := openTestJournal(t)
	base := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		e := journal.Entry{
			Seq:     int64(i + 1),
			TS:      base.Add(time.Duration(i) * time.Second),
			LevelID: "pipes-03",
			Cwd:     "/home/learner",
			Raw:     "cmd" + string(rune('a'+i)),
			Exit:    0,
		}
		if err := j.Append(context.Background(), e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	j.SetLevel("pipes-03", 0)

	var reader verify.JournalReader = j

	cases := []struct {
		name string
		s    verify.Scope
		want []string
	}{
		{"level", verify.Scope{Kind: verify.ScopeLevel}, []string{"cmda", "cmdb", "cmdc"}},
		{"last_n", verify.Scope{Kind: verify.ScopeLastN, N: 2}, []string{"cmdb", "cmdc"}},
		{"last", verify.Scope{Kind: verify.ScopeLast}, []string{"cmdc"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := reader.Commands(c.s)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Commands(%+v) through verify.JournalReader = %v, want %v", c.s, got, c.want)
			}
			if err := j.Err(); err != nil {
				t.Errorf("Err() after Commands(%+v) = %v, want nil", c.s, err)
			}

			// The same call spelled with the lower package's type. Identical
			// because it IS the same type, not because anything translates.
			viaScope := j.Commands(scope.Scope{Kind: c.s.Kind, N: c.s.N})
			if !reflect.DeepEqual(viaScope, got) {
				t.Errorf("Commands(scope.Scope) = %v, Commands(verify.Scope) = %v; want identical", viaScope, got)
			}
		})
	}
}
