package journal_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/journal"
	"github.com/JoottunAtish/ShellForge/internal/store"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// reader adapts a Journal to verify.JournalReader. The adapter exists
// because internal/journal is layer 2 and internal/verify is layer 3, so
// the production package cannot name verify.Scope: see the issue #51 plan,
// section 2, and internal/journal/doc.go. Wiring at layer 4 will carry the
// same three lines.
type reader struct{ j *journal.Journal }

func (r reader) Commands(s verify.Scope) []string {
	return r.j.Commands(journal.Scope{Kind: journal.ScopeKind(s.Kind), N: s.N})
}

var _ verify.JournalReader = reader{}

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

// TestJournalSatisfiesVerifyJournalReader is the behavioural half of the
// acceptance criterion: the compile-time assertion above pins the method
// set, and this proves the adapter translates every verify.Scope kind to
// the same answer the production journal.Scope call would give directly.
func TestJournalSatisfiesVerifyJournalReader(t *testing.T) {
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

	r := reader{j: j}

	cases := []verify.Scope{
		{Kind: verify.ScopeLevel},
		{Kind: verify.ScopeLastN, N: 2},
		{Kind: verify.ScopeLast},
	}
	for _, scope := range cases {
		direct := j.Commands(journal.Scope{Kind: journal.ScopeKind(scope.Kind), N: scope.N})
		viaAdapter := r.Commands(scope)
		if !reflect.DeepEqual(direct, viaAdapter) {
			t.Errorf("scope %+v: direct = %v, via adapter = %v", scope, direct, viaAdapter)
		}
	}
}

// TestJournalScopeMirrorsVerifyScope closes the drift hole the mirrored
// type approach otherwise leaves open. If this test fails, verify.Scope or
// verify.ScopeKind changed shape or value and internal/journal.Scope was
// not updated to match: see the issue #51 plan, section 2, for why the
// mirror exists instead of a shared type, and section 14 for the follow-up
// that would remove the need for this test entirely.
func TestJournalScopeMirrorsVerifyScope(t *testing.T) {
	vType := reflect.TypeOf(verify.Scope{})
	jType := reflect.TypeOf(journal.Scope{})

	if vType.NumField() != jType.NumField() {
		t.Fatalf("verify.Scope has %d fields, journal.Scope has %d; see issue #51 plan section 2",
			vType.NumField(), jType.NumField())
	}
	for i := 0; i < vType.NumField(); i++ {
		vf := vType.Field(i)
		jf := jType.Field(i)
		if vf.Name != jf.Name {
			t.Errorf("field %d: verify.Scope has %q, journal.Scope has %q; see issue #51 plan section 2", i, vf.Name, jf.Name)
		}
	}

	if reflect.TypeOf(verify.ScopeKind("")).Kind() != reflect.String {
		t.Error("verify.ScopeKind is no longer backed by string; see issue #51 plan section 2")
	}
	if reflect.TypeOf(journal.ScopeKind("")).Kind() != reflect.String {
		t.Error("journal.ScopeKind is no longer backed by string; see issue #51 plan section 2")
	}

	vN, vOK := vType.FieldByName("N")
	jN, jOK := jType.FieldByName("N")
	if !vOK || !jOK {
		t.Fatal("Scope.N is missing on one side; see issue #51 plan section 2")
	}
	if vN.Type.Kind() != reflect.Int || jN.Type.Kind() != reflect.Int {
		t.Error("Scope.N is no longer an int on one side; see issue #51 plan section 2")
	}

	pairs := []struct {
		name string
		v    verify.ScopeKind
		j    journal.ScopeKind
	}{
		{"ScopeLevel", verify.ScopeLevel, journal.ScopeLevel},
		{"ScopeLastN", verify.ScopeLastN, journal.ScopeLastN},
		{"ScopeLast", verify.ScopeLast, journal.ScopeLast},
	}

	// wantScopeKinds pins the set SIZE, not just the members listed above.
	// Update this alongside adding the new kind to both journal.ScopeKind
	// and journal.commands' switch before bumping it: a scope kind that
	// exists in verify but falls through journal.commands' default arm
	// degrades silently (an empty command list, never an error a check can
	// see), which is exactly the failure mode this whole file exists to
	// catch.
	const wantScopeKinds = 3
	if len(pairs) != wantScopeKinds {
		t.Fatalf("this test enumerates %d scope kinds, want %d; a new verify.ScopeKind was added without updating this pairs table", len(pairs), wantScopeKinds)
	}

	for _, p := range pairs {
		if string(p.v) != string(p.j) {
			t.Errorf("%s: verify has %q, journal has %q; see issue #51 plan section 2", p.name, string(p.v), string(p.j))
		}
	}
}
