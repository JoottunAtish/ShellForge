package runtimetest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"
)

// packageDir returns the absolute directory of this package, so the
// source-parsing guards below can find contract.go and its siblings without
// depending on the working directory the test binary happens to run from.
func packageDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to report this file's path")
	}
	return filepath.Dir(thisFile)
}

// parseContractFile parses contract.go with go/ast.
func parseContractFile(t *testing.T) *ast.File {
	t.Helper()
	path := filepath.Join(packageDir(t), "contract.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return f
}

// packageSourceFiles parses every non-test *.go file in this package's
// directory, so a completeness guard built on it has no blind spot for a
// thirteenth assertion declared outside contract.go. _test.go files are
// excluded on purpose: they are the suite's own tests, not assertions the
// dispatch table is supposed to register.
func packageSourceFiles(t *testing.T) []*ast.File {
	t.Helper()
	dir := packageDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		files = append(files, f)
	}
	return files
}

// typeString renders a type expression the way it would be written in
// source, for the narrow set of forms an assertion signature can use.
func typeString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	default:
		return fmt.Sprintf("%T", e)
	}
}

// isAssertionSignature reports whether ft matches func(t *testing.T, f Factory).
func isAssertionSignature(ft *ast.FuncType) bool {
	if ft.Results != nil && len(ft.Results.List) > 0 {
		return false
	}
	if ft.Params == nil || len(ft.Params.List) != 2 {
		return false
	}
	first, second := ft.Params.List[0], ft.Params.List[1]
	if len(first.Names) != 1 || typeString(first.Type) != "*testing.T" {
		return false
	}
	if len(second.Names) != 1 || typeString(second.Type) != "Factory" {
		return false
	}
	return true
}

// topLevelFuncs returns every top level (non-method) function declaration in
// file, keyed by name.
func topLevelFuncs(file *ast.File) map[string]*ast.FuncDecl {
	decls := make(map[string]*ast.FuncDecl)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		decls[fn.Name.Name] = fn
	}
	return decls
}

// sourceAssertionNames returns the name of every exported function declared
// anywhere in this package's non-test files whose signature matches an
// assertion: func(t *testing.T, f Factory). It is not limited to
// contract.go: a thirteenth assertion declared in any other non-test file
// would otherwise be exported, callable, and invisible to this check.
func sourceAssertionNames(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, file := range packageSourceFiles(t) {
		for name, fn := range topLevelFuncs(file) {
			if !fn.Name.IsExported() || !strings.HasPrefix(name, "Test") {
				continue
			}
			if isAssertionSignature(fn.Type) {
				names = append(names, name)
			}
		}
	}
	return names
}

// TestContractTableIsComplete proves that the unexported dispatch table in
// contract.go lists every exported assertion function exactly once, with no
// omission and no duplicate. This is the regression this guard exists for:
// somebody adds a thirteenth assertion and forgets to register it, or
// registers one twice under two names.
func TestContractTableIsComplete(t *testing.T) {
	if len(contract) != 12 {
		t.Fatalf("dispatch table length: want 12, got %d", len(contract))
	}

	seen := make(map[string]bool, len(contract))
	tableNames := make(map[string]bool, len(contract))
	for _, a := range contract {
		if seen[a.name] {
			t.Errorf("dispatch table has a duplicate subtest name %q", a.name)
		}
		seen[a.name] = true

		fn := goruntime.FuncForPC(reflect.ValueOf(a.run).Pointer())
		if fn == nil {
			t.Errorf("dispatch table entry %q: could not resolve its function pointer", a.name)
			continue
		}
		full := fn.Name()
		short := full[strings.LastIndex(full, ".")+1:]
		want := "Test" + a.name
		if short != want {
			t.Errorf("dispatch table entry %q: run resolves to %s, want %s", a.name, full, want)
		}
		tableNames[want] = true
	}

	sourceNames := make(map[string]bool)
	for _, n := range sourceAssertionNames(t) {
		sourceNames[n] = true
	}

	for n := range sourceNames {
		if !tableNames[n] {
			t.Errorf("contract.go declares %s but the dispatch table does not register it", n)
		}
	}
	for n := range tableNames {
		if !sourceNames[n] {
			t.Errorf("dispatch table registers %s but contract.go does not declare it as an assertion", n)
		}
	}
}

// TestAssertionSignatures pins the exact signature of every assertion the
// ticket names: an exported func Test<Name>(t *testing.T, f Factory) with no
// results, declared in contract.go.
func TestAssertionSignatures(t *testing.T) {
	decls := topLevelFuncs(parseContractFile(t))

	names := []string{
		"ProvisionIsIdempotent",
		"StatusReportsProvisioned",
		"ExecCapturesStreamsSeparately",
		"ExecReportsExitCode",
		"ExecHonoursContextCancellation",
		"ExecRunsAsRequestedUser",
		"PushFilesMaterializesContentModeOwner",
		"PushFilesStripsCarriageReturns",
		"PullFileRoundTrips",
		"DestroyThenStatusReportsMissing",
		"CapabilitiesAreSelfConsistent",
		"NoNetworkByDefault",
	}

	for _, name := range names {
		name := name
		t.Run(name, func(t *testing.T) {
			fn, ok := decls["Test"+name]
			if !ok {
				t.Fatalf("contract.go does not declare an exported func Test%s", name)
			}
			if !fn.Name.IsExported() {
				t.Fatalf("Test%s is declared but is not exported", name)
			}
			if !isAssertionSignature(fn.Type) {
				t.Fatalf("Test%s does not have the signature (t *testing.T, f Factory) with no results", name)
			}
		})
	}
}

// modulePath is this module's import path, per go.mod. Any import that
// starts with this prefix is module-internal.
const modulePath = "github.com/JoottunAtish/ShellForge"

// runtimePackagePath is the one module-internal import this package is
// permitted to make: internal/runtime itself.
const runtimePackagePath = modulePath + "/internal/runtime"

// deniedStdlib names standard library packages that are forbidden here even
// though the allowlist below only governs module-internal imports. Both
// packages let this test-infrastructure code reach outside the sandbox
// contract: os/exec can spawn a host process, net/http can make a network
// call, and neither belongs in a package whose only job is to drive a
// Runtime through its documented interface.
var deniedStdlib = []string{"net/http", "os/exec"}

// checkImport reports the forbidden import in p, or "" if p is allowed.
// This package is only ever permitted to import the standard library and
// internal/runtime itself: an allowlist naming the one module-internal
// import that is fine, rather than a denylist naming known-bad ones, so a
// new backend package or a renamed one is caught by default instead of
// passing silently.
func checkImport(p string) string {
	for _, bad := range deniedStdlib {
		if p == bad {
			return p
		}
	}
	isModuleInternal := p == modulePath || strings.HasPrefix(p, modulePath+"/")
	if isModuleInternal && p != runtimePackagePath {
		return p
	}
	return ""
}

// TestPackageImportsNoBackend parses every .go file in this package,
// including _test.go files, and asserts none of them imports anything but
// the standard library and internal/runtime. internal/archtest already
// enforces the non-test half of this; this guard additionally covers
// _test.go files, which archtest deliberately skips and which is exactly
// where a fake backend import would most plausibly land.
func TestPackageImportsNoBackend(t *testing.T) {
	dir := packageDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name)
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				if checkImport(p) == "" {
					continue
				}
				if p == modulePath || strings.HasPrefix(p, modulePath+"/") {
					t.Errorf("%s imports %q: runtimetest may import only the standard library and %q, not another module-internal package", name, p, runtimePackagePath)
				} else {
					t.Errorf("%s imports %q, which is denied here even though it is standard library", name, p)
				}
			}
		})
	}
}
