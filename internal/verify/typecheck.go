package verify

// TypeChecker answers what a pack validator needs to know about a check type
// without importing this package's registry directly.
//
// It satisfies the interface of the same name that internal/content declares.
// The interface is declared there, in the consuming package, and this is the
// implementation: internal/content and this package are both layer 3, and
// neither imports the other. cmd/shellforge is what puts the two together,
// which is the whole point of declaring the interface at the point of use.
type TypeChecker struct{}

// Known reports whether typeName is a registered check type.
func (TypeChecker) Known(typeName string) bool {
	_, ok := Lookup(typeName)
	return ok
}

// ValidateParams reports whether params is a well-formed parameter set for
// typeName.
//
// It answers by building the check and throwing it away, which is the only
// honest answer available: a Factory is where a type's parameter rules live,
// and duplicating them here would create a second copy to drift. Building is
// cheap and side-effect free, because a Factory parses parameters and never
// touches the sandbox; only Check.Run does that, and Run is not called here.
func (TypeChecker) ValidateParams(typeName string, params map[string]any) error {
	factory, ok := Lookup(typeName)
	if !ok {
		return factoryError(typeName, "", errUnknownCheckType)
	}
	_, err := factory(Spec{Type: typeName, Params: params})
	return err
}

// errUnknownCheckType is returned when ValidateParams is asked about a type
// that is not registered. A caller is expected to have asked Known first, so
// this is a defensive answer rather than the normal path.
var errUnknownCheckType = errUnknownType{}

// errUnknownType is a distinct type so a caller can tell "no such check type"
// apart from "this check type rejected your parameters" with errors.As.
type errUnknownType struct{}

func (errUnknownType) Error() string { return "no such check type" }
