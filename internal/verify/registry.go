package verify

import (
	"fmt"
	"sort"
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
// Register panics on a duplicate type name. A duplicate is a programming
// error caught at process startup, not a condition a caller can usefully
// recover from: two Factory functions racing to answer the same type name
// is exactly the kind of mistake that must fail loudly and immediately,
// never quietly pick one and hide the other.
func Register(typeName string, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()

	if _, exists := registry[typeName]; exists {
		panic(fmt.Sprintf("verify: check type %q registered twice", typeName))
	}
	registry[typeName] = f
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
