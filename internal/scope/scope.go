package scope

// ScopeKind selects which slice of journal history a journal check reads.
//
// The name repeats the package name, which Go style normally discourages,
// because internal/verify aliases this type as verify.ScopeKind and
// internal/journal reads it as scope.ScopeKind. Naming it Kind here would
// make the alias read verify.ScopeKind = scope.Kind, which hides that the
// two are the same type behind two unrelated-looking names.
type ScopeKind string

const (
	// Level is every command recorded for the level and attempt currently
	// under verification. What "currently" means is the reading package's
	// contract, not this one's: see internal/journal's SetLevel.
	Level ScopeKind = "level"

	// LastN is the most recent N commands.
	LastN ScopeKind = "last_n"

	// Last is only the most recent command.
	Last ScopeKind = "last"
)

// Scope selects which commands a journal check reads. N is meaningful only
// when Kind is LastN.
type Scope struct {
	Kind ScopeKind
	N    int
}
