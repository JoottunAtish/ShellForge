package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// bannedFilesystemRemoval lists the os functions this package's own
// production source must never call. os.Chmod is deliberately absent from
// this list: internal/store/store.go calls it once, to restrict a database
// file's permissions after creating it, which only ever narrows access and
// never destroys anything. Every other name here can delete, truncate, or
// silently overwrite a file, and this package's stance (see doc.go) is that
// it destroys nothing beyond the two-column schema_version table it owns.
var bannedFilesystemRemoval = map[string]bool{
	"Remove":    true,
	"RemoveAll": true,
	"Truncate":  true,
	"Rename":    true,
	"WriteFile": true,
	"Create":    true,
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
// performed by a helper in another package, an indirect call through a
// function value or interface, or anything a build tag hides from the glob.
// It exists so that a future "self-healing" os.Remove of a corrupt database
// fails a test instead of shipping quietly.
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

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch ident.Name {
			case "os":
				if bannedFilesystemRemoval[sel.Sel.Name] {
					t.Errorf("%s calls os.%s, which this package must never call (see doc.go)", name, sel.Sel.Name)
				}
			case "exec":
				if sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext" {
					t.Errorf("%s calls exec.%s; internal/store must never spawn a subprocess", name, sel.Sel.Name)
				}
			}
			return true
		})
	}
}

// TestDocAnchorsHaveTroubleshootingHeadings asserts that every DocAnchor
// constant this package declares has a matching heading in
// docs/05-troubleshooting.md. ux.Fail passes the anchor as a positional
// argument here, which the CI Docs job's `DocAnchor: "..."` grep cannot see,
// so this is the honest local guard for this package's four anchors.
func TestDocAnchorsHaveTroubleshootingHeadings(t *testing.T) {
	anchors := []string{anchorUnwritable, anchorCorrupt, anchorTooNew, anchorMigrationFailed}

	root := storeModuleRoot(t)
	content, err := os.ReadFile(filepath.Join(root, "docs", "05-troubleshooting.md"))
	if err != nil {
		t.Fatalf("read docs/05-troubleshooting.md: %v", err)
	}
	lines := strings.Split(string(content), "\n")

	for _, anchor := range anchors {
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
