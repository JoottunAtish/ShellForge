package sandbox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The doc anchor gate for this package, mirroring
// cmd/shellforge/docanchor_test.go and internal/doctor/anchors_test.go.
//
// The Docs job in .github/workflows/ci.yml finds anchors by grepping Go source
// for the literal text `DocAnchor: "..."`, which only matches a struct literal
// field assignment. Every ux.Fail call in this package passes its anchor as
// the fourth positional argument instead, so that CI grep cannot see a single
// one of them. This test parses the package and finds every ux.Fail call site
// itself, so a new call site with a typo'd anchor fails this test on the
// commit that introduces it, with no bookkeeping required.

// uxFailAnchorArg is the zero-based index of the docAnchor parameter in
// ux.Fail(op string, err error, remediation, docAnchor string).
const uxFailAnchorArg = 3

// TestEveryDocAnchorInThisPackageHasAHeading asserts that every anchor this
// package hands to ux.Fail resolves to a heading in docs/05-troubleshooting.md.
//
// An empty anchor is legal and means no page applies, so it is skipped
// rather than reported. An anchor naming a package-level string constant is
// resolved, because a constant is exactly as verifiable as a literal.
// Anything else is reported as unverifiable rather than passed over in
// silence: this test cannot follow it, and neither can the CI grep.
func TestEveryDocAnchorInThisPackageHasAHeading(t *testing.T) {
	anchors, unverifiable := collectSandboxUxFailAnchors(t)

	for _, expr := range unverifiable {
		t.Errorf("ux.Fail at %s passes a docAnchor this test cannot read.\n"+
			"Pass a string literal, or a constant declared in this package and inlined here, "+
			"so that the anchor is verifiable at build time.", expr)
	}

	if len(anchors) == 0 {
		t.Fatal("no ux.Fail anchors found in this package; the AST walk is looking in the wrong place")
	}

	headings := sandboxTroubleshootingHeadings(t)

	for _, anchor := range anchors {
		if !headings.MatchString(anchor) {
			t.Errorf("doc anchor %q has no heading in docs/05-troubleshooting.md.\n"+
				"Add a `## %s` section there in the same commit, or reuse an anchor that exists.",
				anchor, anchor)
		}
	}
}

// collectSandboxUxFailAnchors parses every non-test .go file in this
// directory and returns the distinct non-empty string-literal anchors
// passed to ux.Fail, plus the positions of any call whose anchor is not a
// string literal.
//
// Test files are skipped on purpose. A test that constructs a ux.Error to
// assert on rendering is not a call site a learner can ever reach, and
// holding tests to the heading contract would mean inventing headings for
// fixtures.
func collectSandboxUxFailAnchors(t *testing.T) (anchors []string, unverifiable []string) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}

	fset := token.NewFileSet()
	seen := map[string]bool{}

	files := make([]*ast.File, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, file)
	}

	consts := sandboxStringConstants(files)

	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isSandboxUxFail(call.Fun) {
				return true
			}
			if len(call.Args) <= uxFailAnchorArg {
				unverifiable = append(unverifiable, fset.Position(call.Pos()).String())
				return true
			}

			value, ok := sandboxStringValue(call.Args[uxFailAnchorArg], consts)
			if !ok {
				unverifiable = append(unverifiable, fset.Position(call.Pos()).String())
				return true
			}
			if value != "" && !seen[value] {
				seen[value] = true
				anchors = append(anchors, value)
			}
			return true
		})
	}

	sort.Strings(anchors)
	return anchors, unverifiable
}

// sandboxStringValue resolves expr to a string, either as a literal or
// through a package-level constant, and reports whether it could.
func sandboxStringValue(expr ast.Expr, consts map[string]string) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		return value, true
	case *ast.Ident:
		value, ok := consts[e.Name]
		return value, ok
	default:
		return "", false
	}
}

// sandboxStringConstants collects every package-level constant whose value
// is a plain string literal, keyed by name. Only const declarations are
// read, never var: a var can be reassigned before the call site runs.
func sandboxStringConstants(files []*ast.File) map[string]string {
	consts := map[string]string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range value.Names {
					if i >= len(value.Values) {
						continue
					}
					lit, ok := value.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					if unquoted, err := strconv.Unquote(lit.Value); err == nil {
						consts[name.Name] = unquoted
					}
				}
			}
		}
	}
	return consts
}

// isSandboxUxFail reports whether fun names ux.Fail, matching the selector
// form only, which is how this package always calls it.
func isSandboxUxFail(fun ast.Expr) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Fail" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == "ux"
}

// sandboxTroubleshootingHeadings returns a pattern matching any heading
// line in docs/05-troubleshooting.md, in the same loose, substring-matching
// shape the CI Docs job uses.
func sandboxTroubleshootingHeadings(t *testing.T) *sandboxHeadingSet {
	t.Helper()

	root := sandboxModuleRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "05-troubleshooting.md"))
	if err != nil {
		t.Fatalf("read docs/05-troubleshooting.md: %v", err)
	}

	var headings []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if regexp.MustCompile(`^#{1,4}\s+`).MatchString(line) {
			headings = append(headings, line)
		}
	}
	if len(headings) == 0 {
		t.Fatal("docs/05-troubleshooting.md has no headings; the parse is wrong")
	}
	return &sandboxHeadingSet{headings: headings}
}

// sandboxHeadingSet answers whether any heading mentions an anchor.
type sandboxHeadingSet struct {
	headings []string
}

// MatchString reports whether any heading contains anchor.
func (h *sandboxHeadingSet) MatchString(anchor string) bool {
	for _, heading := range h.headings {
		if strings.Contains(heading, anchor) {
			return true
		}
	}
	return false
}

// sandboxModuleRoot walks up from the test's working directory until it
// finds go.mod, matching cmd/shellforge/docanchor_test.go's own helper.
func sandboxModuleRoot(t *testing.T) string {
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

// TestThisPackageDoesNotDotImportUx holds the one assumption
// TestEveryDocAnchorInThisPackageHasAHeading rests on: it recognises
// ux.Fail by the selector `ux.Fail`, so a dot-import would make every call
// site invisible to it.
func TestThisPackageDoesNotDotImportUx(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			if imp.Name != nil && imp.Name.Name == "." {
				t.Errorf("%s dot-imports %s; the doc anchor test recognises ux.Fail by selector and cannot see a dot-imported call",
					name, imp.Path.Value)
			}
		}
	}
}
