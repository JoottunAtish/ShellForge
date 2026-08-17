package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// bannedFilesystemRemoval lists the os functions this package's own
// production source must never even name, whether called directly or taken
// as a function value (`var rm = os.Remove` is caught exactly like
// `os.Remove(p)`, since both are the same *ast.SelectorExpr). os.Chmod is
// deliberately absent from this list: internal/store/store.go calls it
// once, to restrict a database file's permissions after creating it, which
// only ever narrows access and never destroys anything. os.OpenFile is also
// absent, because this package's own permission probe legitimately opens
// the database with os.O_RDWR and no os.O_CREATE or os.O_TRUNC: see
// openFileRequestsTruncateOrCreate below for the narrower, flag-aware check
// that call gets instead of a flat ban. Every other name here can delete,
// truncate, or silently overwrite a file, and this package's stance (see
// doc.go) is that it destroys nothing beyond the single-column
// schema_version table it owns.
var bannedFilesystemRemoval = map[string]bool{
	"Remove":    true,
	"RemoveAll": true,
	"Truncate":  true,
	"Rename":    true,
	"WriteFile": true,
	"Create":    true,
}

// bannedSyscall lists syscall functions this package's own production
// source must never name, for the same reason and by the same rule as
// bannedFilesystemRemoval: both delete or truncate a file underneath
// whatever os or database/sql abstraction sits on top of it.
var bannedSyscall = map[string]bool{
	"Unlink":    true,
	"Ftruncate": true,
}

// importLocalName returns the identifier production code in f uses to name
// importPath: the alias from an `import alias "path"` line if one was
// written, or the import path's own final segment otherwise. It returns ""
// when f does not import importPath at all, which callers treat as "this
// file cannot possibly reference that package", closing the evasion where
// a matcher keyed on the literal string "os" is blind to `import osx "os"`.
//
// A dot import ("import . \"os\"") is out of scope: it would make every
// bare identifier in the file a candidate, which no call site in this
// codebase does and which gofmt-adjacent convention already discourages.
func importLocalName(f *ast.File, importPath string) string {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != importPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		if idx := strings.LastIndex(importPath, "/"); idx >= 0 {
			return importPath[idx+1:]
		}
		return importPath
	}
	return ""
}

// openFileRequestsTruncateOrCreate reports whether expr, the flag argument
// of an os.OpenFile call, mentions osAlias.O_TRUNC or osAlias.O_CREATE
// anywhere in a chain of bitwise-OR'd flags. This is what lets the existing
// read-only probe in classifyOpenError (os.O_RDWR, no O_CREATE, no
// O_TRUNC) pass while a future `os.OpenFile(path, os.O_RDWR|os.O_TRUNC,
// 0o600)` fails this test instead of shipping quietly.
func openFileRequestsTruncateOrCreate(expr ast.Expr, osAlias string) bool {
	switch v := expr.(type) {
	case *ast.ParenExpr:
		return openFileRequestsTruncateOrCreate(v.X, osAlias)
	case *ast.BinaryExpr:
		return openFileRequestsTruncateOrCreate(v.X, osAlias) || openFileRequestsTruncateOrCreate(v.Y, osAlias)
	case *ast.SelectorExpr:
		ident, ok := v.X.(*ast.Ident)
		return ok && ident.Name == osAlias && (v.Sel.Name == "O_TRUNC" || v.Sel.Name == "O_CREATE")
	default:
		return false
	}
}

// storeProductionFiles lists this package's own non-test .go files.
func storeProductionFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package directory: %v", err)
	}
	var out []string
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	return out
}

// TestStorePackageCallsNoFilesystemRemoval is a source level tripwire, not a
// proof: it is package-local and source-level, so it cannot see a removal
// performed by a helper in another package, an indirect call through an
// interface, or anything a build tag hides from the glob. It resolves each
// file's own import aliases rather than matching the literal identifiers
// "os", "exec", and "syscall", so `import osx "os"; osx.Remove(p)` is caught
// exactly like `os.Remove(p)`. It matches on bare *ast.SelectorExpr nodes,
// not only when one is a CallExpr's Fun, so a banned function taken as a
// value (`var rm = os.Remove` followed by `rm(path)`) is caught too: a
// CallExpr's Fun is itself visited as a SelectorExpr by the same walk, so
// this one check covers both shapes without duplicating it. It exists so
// that a future "self-healing" os.Remove of a corrupt database fails a test
// instead of shipping quietly.
func TestStorePackageCallsNoFilesystemRemoval(t *testing.T) {
	fset := token.NewFileSet()
	for _, name := range storeProductionFiles(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == "os/exec" {
				t.Errorf("%s imports os/exec; internal/store must never spawn a subprocess", name)
			}
		}

		osAlias := importLocalName(f, "os")
		execAlias := importLocalName(f, "os/exec")
		syscallAlias := importLocalName(f, "syscall")

		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CallExpr:
				sel, ok := v.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if ident.Name == osAlias && sel.Sel.Name == "OpenFile" && len(v.Args) >= 2 {
					if openFileRequestsTruncateOrCreate(v.Args[1], osAlias) {
						t.Errorf("%s calls os.OpenFile with O_TRUNC or O_CREATE in its flags, which this package must never do (see doc.go): the one permitted os.OpenFile call is a read-only probe with no O_CREATE and no O_TRUNC", name)
					}
				}
				return true

			case *ast.SelectorExpr:
				ident, ok := v.X.(*ast.Ident)
				if !ok {
					return true
				}
				switch ident.Name {
				case osAlias:
					if bannedFilesystemRemoval[v.Sel.Name] {
						t.Errorf("%s references os.%s, which this package must never call (see doc.go), whether called directly or taken as a function value", name, v.Sel.Name)
					}
				case execAlias:
					if v.Sel.Name == "Command" || v.Sel.Name == "CommandContext" {
						t.Errorf("%s references exec.%s; internal/store must never spawn a subprocess", name, v.Sel.Name)
					}
				case syscallAlias:
					if bannedSyscall[v.Sel.Name] {
						t.Errorf("%s references syscall.%s, which this package must never call (see doc.go)", name, v.Sel.Name)
					}
				}
				return true
			}
			return true
		})
	}
}

// declaredDocAnchors walks every production .go file in this package (via
// storeProductionFiles, so a test file's own fixture constants can never
// contribute one) and returns the string value of every constant whose name
// has the "anchor" prefix. It is the AST-derived source of truth
// TestDocAnchorsHaveTroubleshootingHeadings checks against, so that a
// seventh anchor added later is checked automatically instead of depending
// on someone remembering to extend a hardcoded list. See the floor inside
// TestDocAnchorsHaveTroubleshootingHeadings itself for what happens if this
// walk ever goes blind.
func declaredDocAnchors(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	var declared []string
	for _, name := range storeProductionFiles(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Values) != len(vs.Names) {
					continue
				}
				for i, ident := range vs.Names {
					if !strings.HasPrefix(ident.Name, "anchor") {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					value, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("unquote %s in %s: %v", ident.Name, name, err)
					}
					declared = append(declared, value)
				}
			}
		}
	}
	return declared
}

// TestDocAnchorsHaveTroubleshootingHeadings asserts that every DocAnchor
// constant this package declares has a matching heading in
// docs/05-troubleshooting.md. ux.Fail passes the anchor as a positional
// argument here, which the CI Docs job's `DocAnchor: "..."` grep cannot see,
// so this is the honest local guard for this package's anchors.
//
// The anchor list itself comes from declaredDocAnchors's AST walk rather
// than a hardcoded slice, because the global gate cannot see this package's
// positional ux.Fail calls (open issue #86; PR #107 owns that fix, not this
// one), which makes this local test the only witness for every anchor this
// package declares. A hardcoded list inside the only witness is a witness
// that silently shrinks the next time someone adds an anchor and forgets to
// extend it. The six named constants below are kept anyway, as an explicit
// floor: a walk that finds nothing still needs to fail loudly rather than
// pass vacuously over zero anchors, which is exactly the failure mode a
// gate that scans nothing can have (see the testing skill).
func TestDocAnchorsHaveTroubleshootingHeadings(t *testing.T) {
	declared := declaredDocAnchors(t)
	for _, want := range []string{anchorUnwritable, anchorCorrupt, anchorTooNew, anchorMigrationFailed, anchorInUse, anchorForeignDatabase} {
		if !slices.Contains(declared, want) {
			t.Errorf("declaredDocAnchors did not find %q in this package's own source: the walk has gone blind", want)
		}
	}

	root := storeModuleRoot(t)
	content, err := os.ReadFile(filepath.Join(root, "docs", "05-troubleshooting.md"))
	if err != nil {
		t.Fatalf("read docs/05-troubleshooting.md: %v", err)
	}
	lines := strings.Split(string(content), "\n")

	for _, anchor := range declared {
		pattern := regexp.MustCompile(`^#{1,4}\s+.*` + regexp.QuoteMeta(anchor))
		found := false
		for _, line := range lines {
			if pattern.MatchString(strings.TrimSpace(line)) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no heading for anchor %q in docs/05-troubleshooting.md", anchor)
		}
	}
}

// storeModuleRoot walks up from the test's working directory until it finds
// go.mod, matching internal/archtest's own moduleRoot helper.
func storeModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}
