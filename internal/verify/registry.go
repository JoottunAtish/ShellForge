package verify

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register adds f under typeName, so that a level's checks[].type can build
// one. It is meant to be called from an init function in the *_checks.go
// file that owns the type.
//
// knownParams names every parameter the type accepts, required or optional.
// Register wraps f so that a parameter outside that set is refused before f
// is ever called. Enforcing it here rather than inside each Factory is what
// makes the rule hold for a check type nobody has written yet: a Factory that
// simply never reads a key cannot tell a typo apart from a key it does not
// care about, and every Factory forgetting the same thing is how twelve of
// them came to accept `sha526` as a spelling of `sha256`.
//
// Register panics on a duplicate type name. A duplicate is a programming
// error caught at process startup, not a condition a caller can usefully
// recover from: two Factory functions racing to answer the same type name
// is exactly the kind of mistake that must fail loudly and immediately,
// never quietly pick one and hide the other.
func Register(typeName string, f Factory, knownParams ...string) {
	registryMu.Lock()
	defer registryMu.Unlock()

	if _, exists := registry[typeName]; exists {
		panic(fmt.Sprintf("verify: check type %q registered twice", typeName))
	}
	registry[typeName] = guardParams(typeName, knownParams, f)
}

// guardParams returns f wrapped in the unknown-parameter rule.
//
// The error names the offending key and lists what the type does accept,
// because an author who mistyped one of eight parameter names needs to see
// the eight, not be told that one of them was wrong.
func guardParams(typeName string, knownParams []string, f Factory) Factory {
	known := make(map[string]bool, len(knownParams))
	for _, p := range knownParams {
		known[p] = true
	}

	return func(s Spec) (Check, error) {
		var unknown []string
		for key := range s.Params {
			if !known[key] {
				unknown = append(unknown, key)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			accepted := "no parameters at all"
			if len(knownParams) > 0 {
				sorted := append([]string(nil), knownParams...)
				sort.Strings(sorted)
				accepted = strings.Join(sorted, ", ")
			}
			return nil, factoryError(typeName, s.ID, fmt.Errorf(
				"unknown param %s; a %s check accepts %s",
				strings.Join(quoteAll(unknown), ", "), typeName, accepted))
		}
		return f(s)
	}
}

// quoteAll quotes every name so an error message reads "typo" rather than
// typo, which matters when the offending key is an empty string or carries
// trailing whitespace from the YAML.
func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strconv.Quote(n)
	}
	return out
}

// Lookup returns the Factory registered for typeName, and whether one
// exists.
func Lookup(typeName string) (Factory, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	f, ok := registry[typeName]
	return f, ok
}

// Types returns every registered type name, sorted. The pack validator uses
// it to report an unknown type, and tests use it to prove every registered
// type has coverage.
func Types() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	out := make([]string, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
