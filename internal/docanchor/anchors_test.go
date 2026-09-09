package docanchor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// troubleshootingPath is the living contract this package checks call sites
// against.
const troubleshootingPath = "docs/05-troubleshooting.md"

// uxImportPath is what a file must import to reach ux.Fail or ux.Error from
// outside the ux package itself.
const uxImportPath = "github.com/JoottunAtish/ShellForge/internal/platform/ux"

// parsedFile is one non-test .go file, parsed, together with how THIS file
// (import bindings are per file, not per package) refers to package ux.
type parsedFile struct {
	ast    *ast.File
	uxBind uxBinding
}

// uxBinding describes how one file refers to package ux: qualified under a
// local name (the ordinary case, "ux" unless the import is aliased),
// unqualified because the file dot-imports it, or unqualified because the
// file IS internal/platform/ux and Fail/Error are its own declarations.
//
// Matching on the literal identifier "ux" was the bug this type fixes: an
// aliased import, `uxpkg "internal/platform/ux"`, or a dot import made a
// real ux.Fail call site invisible to the old, identifier-text-only match.
// Nothing in this repository does either today, but "not broken, just
// unable to notice" is exactly the state issue #86 was filed to fix, so the
// binding is computed from the actual import rather than assumed.
type uxBinding struct {
	localName   string // e.g. "ux", or an alias; empty when dot or samePackage
	dot         bool
	samePackage bool
}

// reachable reports whether Fail or Error could possibly be reached from
// this file at all. A file that imports nothing of ux, and is not ux
// itself, cannot contain a real call site, so its AST is not worth walking.
func (b uxBinding) reachable() bool {
	return b.dot || b.samePackage || b.localName != ""
}

// isFailCall reports whether fun names Fail as this file would have to
// spell it: the bare identifier when dot-imported or same-package, the
// qualified selector under this file's own local name otherwise.
func (b uxBinding) isFailCall(fun ast.Expr) bool {
	switch v := fun.(type) {
	case *ast.Ident:
		return (b.dot || b.samePackage) && v.Name == "Fail"
	case *ast.SelectorExpr:
		if b.localName == "" {
			return false
		}
		pkg, ok := v.X.(*ast.Ident)
		return ok && pkg.Name == b.localName && v.Sel.Name == "Fail"
	}
	return false
}

// isErrorType is isFailCall's counterpart for the Error{} struct literal.
func (b uxBinding) isErrorType(t ast.Expr) bool {
	switch v := t.(type) {
	case *ast.Ident:
		return (b.dot || b.samePackage) && v.Name == "Error"
	case *ast.SelectorExpr:
		if b.localName == "" {
			return false
		}
		pkg, ok := v.X.(*ast.Ident)
		return ok && pkg.Name == b.localName && v.Sel.Name == "Error"
	}
	return false
}

// computeUxBinding inspects one file's own import declarations. samePackage
// is decided by the caller from the file's directory, since a file never
// imports its own package.
func computeUxBinding(f *ast.File, samePackage bool) uxBinding {
	if samePackage {
		return uxBinding{samePackage: true}
	}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != uxImportPath {
			continue
		}
		switch {
		case imp.Name == nil:
			return uxBinding{localName: "ux"}
		case imp.Name.Name == ".":
			return uxBinding{dot: true}
		case imp.Name.Name == "_":
			return uxBinding{} // a blank import brings no identifier into scope
		default:
			return uxBinding{localName: imp.Name.Name}
		}
	}
	return uxBinding{} // this file does not import ux at all
}

// findDocAnchors walks every non-test .go file under root and reports every
// doc anchor argument it finds.
//
// anchors is the sorted, de-duplicated set of anchor values that resolved to
// something concrete: a string literal, or a package-level string constant
// referenced by name from the same package. An anchor that resolves to the
// empty string is legal, meaning no doc link, and is not included.
//
// unverifiable is one "file:line" entry per anchor argument that resolved to
// neither: a function parameter, a call result, a selector, anything this
// package cannot prove a value for without running the program. Reporting
// these rather than skipping them is the point: a silently-skipped anchor is
// exactly how the grep this package replaces went blind.
//
// Test files are excluded on purpose. A test that constructs a ux.Error to
// assert on rendering is not a call site a learner can ever reach, and
// holding tests to the heading contract would mean inventing headings for
// fixtures.
func findDocAnchors(t *testing.T, root string) (anchors []string, unverifiable []string) {
	t.Helper()

	byPkg := map[string][]parsedFile{}

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "bin", "dist", "node_modules", "testdata", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}

		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		pkgDir := filepath.Dir(p)
		samePackage := strings.HasSuffix(filepath.ToSlash(pkgDir), "internal/platform/ux")
		byPkg[pkgDir] = append(byPkg[pkgDir], parsedFile{ast: f, uxBind: computeUxBinding(f, samePackage)})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	seen := map[string]bool{}

	for _, files := range byPkg {
		consts := packageStringConsts(files)

		for _, f := range files {
			if !f.uxBind.reachable() {
				continue // this file cannot contain a real ux.Fail or ux.Error site
			}
			ast.Inspect(f.ast, func(n ast.Node) bool {
				expr, ok := docAnchorArg(n, f.uxBind)
				if !ok {
					return true
				}
				if expr == nil {
					// A recognised Fail(...) call or Error{} literal whose
					// anchor argument this package could not even locate,
					// e.g. ux.Fail(spreadArgs()...). Reported rather than
					// silently passed over, for the same reason any other
					// unresolvable anchor is: a silently skipped anchor is
					// exactly how the grep this package replaces went blind.
					unverifiable = append(unverifiable, position(fset, n.Pos()))
					return true
				}
				value, resolvable := resolveString(expr, consts)
				if !resolvable {
					if f.uxBind.samePackage {
						// ux.Fail itself constructs &Error{..., DocAnchor:
						// docAnchor, ...} from its own parameter. That is
						// plumbing, not a real emission site: every anchor
						// it can carry is already checked at the
						// ux.Fail(...) call site that supplied it.
						return true
					}
					unverifiable = append(unverifiable, position(fset, expr.Pos()))
					return true
				}
				if value != "" && !seen[value] {
					seen[value] = true
					anchors = append(anchors, value)
				}
				return true
			})
		}
	}

	sort.Strings(anchors)
	sort.Strings(unverifiable)
	return anchors, unverifiable
}

// packageStringConsts collects every package-level `const name = "value"`
// declaration across files, keyed by name.
//
// Only the single-name, single-value, string-literal shape is resolved.
// Every anchor constant in this repository is written that way; anything
// fancier (iota, a computed value) is left unresolved, which is a safe
// default: an anchor argument referencing it becomes unverifiable rather
// than silently wrong.
func packageStringConsts(files []parsedFile) map[string]string {
	consts := map[string]string{}
	for _, f := range files {
		for _, decl := range f.ast.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				consts[vs.Names[0].Name] = value
			}
		}
	}
	return consts
}

// docAnchorArg reports the expression carrying the doc anchor argument at n,
// if n is one of the two shapes this package recognises for pkg, the
// enclosing file's binding for package ux.
//
// Shape one: ux.Fail(op, err, remediation, anchor), the positional form
// every real call site in this repository uses. Shape two:
// Error{..., DocAnchor: anchor, ...}, the struct-literal form ux.Fail itself
// is built from and the one the old CI grep looked for exclusively.
//
// ok is true whenever n IS a Fail call or an Error composite literal, even
// when no readable anchor expression could be extracted from it (expr is
// then nil): an oddly-shaped real call site, ux.Fail(spreadArgs()...) for
// instance, is reported as unverifiable by the caller rather than silently
// treated as not a call site at all. An Error{} literal that never sets
// DocAnchor is not that case: the field defaults to "", which is the
// already-legal no-doc-link value, so ok is false and there is nothing to
// report.
func docAnchorArg(n ast.Node, pkg uxBinding) (expr ast.Expr, ok bool) {
	switch v := n.(type) {
	case *ast.CallExpr:
		if !pkg.isFailCall(v.Fun) {
			return nil, false
		}
		const docAnchorArgIndex = 3
		if len(v.Args) <= docAnchorArgIndex {
			return nil, true
		}
		return v.Args[docAnchorArgIndex], true

	case *ast.CompositeLit:
		if !pkg.isErrorType(v.Type) {
			return nil, false
		}
		for _, elt := range v.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "DocAnchor" {
				continue
			}
			return kv.Value, true
		}
		return nil, false
	}
	return nil, false
}

// resolveString resolves expr to a string value, either directly as a
// literal or through a same-package constant. ok is false when expr is
// neither, which is the unverifiable case.
//
// expr is always itself a string expression here: docAnchorArg only ever
// returns a positional call argument or a struct field value, and DocAnchor
// is typed string, so there is no address-of form to unwrap.
func resolveString(expr ast.Expr, consts map[string]string) (value string, ok bool) {
	switch v := expr.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.Ident:
		s, found := consts[v.Name]
		return s, found
	default:
		return "", false
	}
}

func position(fset *token.FileSet, pos token.Pos) string {
	p := fset.Position(pos)
	return p.String()
}

// moduleRoot walks up from the working directory to the nearest go.mod,
// matching internal/archtest's own helper: both packages need the real
// repository root and neither may import the other to get it, since
// internal/archtest classifies internal/docanchor's own layer rather than
// the reverse.
func moduleRoot(t *testing.T) string {
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

// headingSet answers whether any heading in docs/05-troubleshooting.md
// mentions an anchor, matching the CI Docs job's own loose rule: a heading
// of `## docker-daemon-down` satisfies an anchor of `daemon-down` too.
// Diverging from the rule that actually decides whether the page has an
// answer would be worse than matching it loosely.
type headingSet struct {
	headings []string
}

// hasAnchor reports whether anchor appears as a substring of any heading
// line. The direction matters: it is the anchor found inside the heading,
// never the much longer heading line found inside the short anchor.
func (h *headingSet) hasAnchor(anchor string) bool {
	for _, heading := range h.headings {
		if strings.Contains(heading, anchor) {
			return true
		}
	}
	return false
}

// troubleshootingHeadings collects every one-to-four-hash heading line in
// docs/05-troubleshooting.md.
func troubleshootingHeadings(t *testing.T, root string) *headingSet {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(root, troubleshootingPath))
	if err != nil {
		t.Fatalf("read %s: %v", troubleshootingPath, err)
	}

	headingLine := regexp.MustCompile(`^#{1,4}\s+`)
	var headings []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if headingLine.MatchString(line) {
			headings = append(headings, line)
		}
	}
	if len(headings) == 0 {
		t.Fatalf("%s has no headings at all; the file or the walk is wrong", troubleshootingPath)
	}
	return &headingSet{headings: headings}
}

// TestEveryDocAnchorHasATroubleshootingHeading is the gate itself: every
// anchor a real ux.Fail or ux.Error{} in this module can emit must resolve
// to a heading in docs/05-troubleshooting.md, and every anchor argument must
// be readable at all.
//
// Verified against a deliberate violation before this test was committed,
// the same procedure the layer rule and the punctuation gate were verified
// with on Day 0: a ux.Fail call with an anchor that has no heading was added
// to a scratch file, this test was confirmed to fail and name it, and the
// scratch file was removed.
func TestEveryDocAnchorHasATroubleshootingHeading(t *testing.T) {
	root := moduleRoot(t)
	anchors, unverifiable := findDocAnchors(t, root)
	if len(anchors) == 0 && len(unverifiable) == 0 {
		t.Fatal("found zero doc anchors anywhere in the module; the walk or the detection is broken, " +
			"not that this repository stopped calling ux.Fail")
	}

	for _, pos := range unverifiable {
		t.Errorf("doc anchor at %s is not a string literal or a package-level string constant, "+
			"so this test cannot verify it resolves to a real heading.\n"+
			"Pass a literal, or a const declared in the same package.", pos)
	}

	headings := troubleshootingHeadings(t, root)
	for _, anchor := range anchors {
		if !headings.hasAnchor(anchor) {
			t.Errorf("doc anchor %q has no heading in %s.\n"+
				"Add a `## %s` section there in the same commit, or reuse an anchor that exists.",
				anchor, troubleshootingPath, anchor)
		}
	}
}

// --------------------------------------------------------------------------
// Fixture tests: prove the mechanism itself, independent of what this
// repository's real code currently contains. These would still catch a
// regression even on a day nobody happens to have written a bad anchor.
// --------------------------------------------------------------------------

// writeFixture materializes files under a fresh temp directory and returns
// its path. Each key is a path relative to the returned root.
func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

func TestFindDocAnchorsPositionalLiteral(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a.go": `package pkg

import "github.com/JoottunAtish/ShellForge/internal/platform/ux"

func f() error {
	return ux.Fail("op", nil, "remediation", "some-anchor")
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(unverifiable) != 0 {
		t.Fatalf("unverifiable = %v, want none", unverifiable)
	}
	if len(anchors) != 1 || anchors[0] != "some-anchor" {
		t.Fatalf("anchors = %v, want [some-anchor]", anchors)
	}
}

func TestFindDocAnchorsPackageConst(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a.go": `package pkg

import "github.com/JoottunAtish/ShellForge/internal/platform/ux"

const docAnchorSomething = "const-anchor"

func f() error {
	return ux.Fail("op", nil, "remediation", docAnchorSomething)
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(unverifiable) != 0 {
		t.Fatalf("unverifiable = %v, want none", unverifiable)
	}
	if len(anchors) != 1 || anchors[0] != "const-anchor" {
		t.Fatalf("anchors = %v, want [const-anchor]", anchors)
	}
}

func TestFindDocAnchorsUnresolvableIsReported(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a.go": `package pkg

import "github.com/JoottunAtish/ShellForge/internal/platform/ux"

func f(anchor string) error {
	return ux.Fail("op", nil, "remediation", anchor)
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(anchors) != 0 {
		t.Fatalf("anchors = %v, want none: a function parameter is not a resolvable anchor", anchors)
	}
	if len(unverifiable) != 1 {
		t.Fatalf("unverifiable = %v, want exactly one entry naming the call site", unverifiable)
	}
}

func TestFindDocAnchorsEmptyStringIsLegalAndSkipped(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a.go": `package pkg

import "github.com/JoottunAtish/ShellForge/internal/platform/ux"

func f() error {
	return ux.Fail("op", nil, "remediation", "")
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(anchors) != 0 {
		t.Fatalf("anchors = %v, want none: an empty anchor means no doc link", anchors)
	}
	if len(unverifiable) != 0 {
		t.Fatalf("unverifiable = %v, want none: an empty literal is resolvable, it is not a real anchor", unverifiable)
	}
}

func TestFindDocAnchorsStructLiteralOutsideUx(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a.go": `package pkg

import "github.com/JoottunAtish/ShellForge/internal/platform/ux"

func f() error {
	return &ux.Error{Op: "op", Remediation: "remediation", DocAnchor: "struct-anchor"}
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(unverifiable) != 0 {
		t.Fatalf("unverifiable = %v, want none", unverifiable)
	}
	if len(anchors) != 1 || anchors[0] != "struct-anchor" {
		t.Fatalf("anchors = %v, want [struct-anchor]", anchors)
	}
}

// TestFindDocAnchorsSkipsUxsOwnPlumbing pins the false positive found while
// building this gate: ux.Fail's own bare Error{DocAnchor: docAnchor, ...}
// construction inside internal/platform/ux forwards its caller's parameter
// and must not be reported as unverifiable on every run.
func TestFindDocAnchorsSkipsUxsOwnPlumbing(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"internal/platform/ux/ux.go": `package ux

type Error struct {
	Op, Remediation, DocAnchor string
	Err                        error
}

func Fail(op string, err error, remediation, docAnchor string) *Error {
	return &Error{Op: op, Err: err, Remediation: remediation, DocAnchor: docAnchor}
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(anchors) != 0 {
		t.Fatalf("anchors = %v, want none: nothing here calls Fail", anchors)
	}
	if len(unverifiable) != 0 {
		t.Fatalf("unverifiable = %v, want none: Fail's own construction is plumbing, not an emission site", unverifiable)
	}
}

// TestFindDocAnchorsCatchesAnUnqualifiedFailInsideUx is the other half of
// the case above: a hypothetical helper written inside internal/platform/ux
// that calls the bare Fail(...) with a bad anchor must still be caught,
// exactly the shape the deliberate-violation check used before this test
// suite was committed.
func TestFindDocAnchorsCatchesAnUnqualifiedFailInsideUx(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"internal/platform/ux/ux.go": `package ux

type Error struct {
	Op, Remediation, DocAnchor string
	Err                        error
}

func Fail(op string, err error, remediation, docAnchor string) *Error {
	return &Error{Op: op, Err: err, Remediation: remediation, DocAnchor: docAnchor}
}

func helper() error {
	return Fail("op", nil, "remediation", "bare-call-anchor")
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(unverifiable) != 0 {
		t.Fatalf("unverifiable = %v, want none", unverifiable)
	}
	if len(anchors) != 1 || anchors[0] != "bare-call-anchor" {
		t.Fatalf("anchors = %v, want [bare-call-anchor]", anchors)
	}
}

// TestFindDocAnchorsChecksALiteralInsideUxToo is the review finding that
// narrowed the plumbing suppression: it must exempt Fail's own forwarded
// parameter specifically, not every Error{} literal anywhere in the ux
// package. A hypothetical second constructor writing a real literal anchor
// must still be checked, exactly as it would be anywhere else in the
// module.
func TestFindDocAnchorsChecksALiteralInsideUxToo(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"internal/platform/ux/ux.go": `package ux

type Error struct {
	Op, Remediation, DocAnchor string
	Err                        error
}

func Fail(op string, err error, remediation, docAnchor string) *Error {
	return &Error{Op: op, Err: err, Remediation: remediation, DocAnchor: docAnchor}
}

func namedErrorHelper() *Error {
	return &Error{Op: "op", DocAnchor: "hypothetical-future-literal"}
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(unverifiable) != 0 {
		t.Fatalf("unverifiable = %v, want none", unverifiable)
	}
	if len(anchors) != 1 || anchors[0] != "hypothetical-future-literal" {
		t.Fatalf("anchors = %v, want [hypothetical-future-literal]: a literal DocAnchor elsewhere in "+
			"the ux package must be checked, not skipped along with Fail's own plumbing", anchors)
	}
}

// TestFindDocAnchorsIgnoresAnUnrelatedFail guards the precision half of the
// same fix: a function that merely happens to be named Fail in a package
// that is not internal/platform/ux, called unqualified, must not be treated
// as ux.Fail. Only the qualified form counts outside the ux package.
// TestFindDocAnchorsDetectsAnAliasedImport pins the fix for the review
// finding on #107: matching on the literal identifier "ux" made an aliased
// import invisible. A real call site must not come back as two empty
// slices when an author writes `uxpkg "internal/platform/ux"`.
func TestFindDocAnchorsDetectsAnAliasedImport(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a.go": `package pkg

import uxpkg "github.com/JoottunAtish/ShellForge/internal/platform/ux"

func f() error {
	return uxpkg.Fail("op", nil, "remediation", "aliased-anchor")
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(unverifiable) != 0 {
		t.Fatalf("unverifiable = %v, want none", unverifiable)
	}
	if len(anchors) != 1 || anchors[0] != "aliased-anchor" {
		t.Fatalf("anchors = %v, want [aliased-anchor]: an aliased import must not make the call site invisible", anchors)
	}
}

// TestFindDocAnchorsDetectsADotImport is the other half of the same finding:
// a file outside package ux that dot-imports it can call Fail unqualified,
// and that call site must be found too.
func TestFindDocAnchorsDetectsADotImport(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a.go": `package pkg

import . "github.com/JoottunAtish/ShellForge/internal/platform/ux"

func f() error {
	return Fail("op", nil, "remediation", "dot-import-anchor")
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(unverifiable) != 0 {
		t.Fatalf("unverifiable = %v, want none", unverifiable)
	}
	if len(anchors) != 1 || anchors[0] != "dot-import-anchor" {
		t.Fatalf("anchors = %v, want [dot-import-anchor]: a dot import must not make the call site invisible", anchors)
	}
}

// TestFindDocAnchorsReportsTooFewArguments covers ux.Fail(spreadArgs()...),
// legal Go where a single multi-value call supplies all four parameters.
// len(v.Args) is 1 in that shape, and the anchor is neither present as a
// literal nor absent in a way that means "not a Fail call": it is a real
// call site this package cannot read, so it must be reported rather
// than silently treated as no call site at all.
func TestFindDocAnchorsReportsTooFewArguments(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a.go": `package pkg

import "github.com/JoottunAtish/ShellForge/internal/platform/ux"

func parts() (string, error, string, string) {
	return "op", nil, "remediation", "spread-anchor"
}

func f() error {
	return ux.Fail(parts())
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(anchors) != 0 {
		t.Fatalf("anchors = %v, want none: this package cannot read an argument list built from a spread call", anchors)
	}
	if len(unverifiable) != 1 {
		t.Fatalf("unverifiable = %v, want exactly one entry naming the call site", unverifiable)
	}
}

func TestFindDocAnchorsIgnoresAnUnrelatedFail(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a.go": `package pkg

func Fail(op, err, remediation, anchor string) string { return anchor }

func f() string {
	return Fail("op", "err", "remediation", "not-a-doc-anchor")
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(anchors) != 0 || len(unverifiable) != 0 {
		t.Fatalf("anchors = %v, unverifiable = %v, want both empty: this Fail is not ux.Fail", anchors, unverifiable)
	}
}

// TestFindDocAnchorsIgnoresComments is the acceptance criterion stated
// directly: an anchor mentioned only in a comment must not count as an
// emitted anchor, which is precisely how the CI grep this package replaces
// went blind for months.
func TestFindDocAnchorsIgnoresComments(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a.go": `package pkg

// DocAnchor: "comment-only-anchor"
func f() {}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(anchors) != 0 || len(unverifiable) != 0 {
		t.Fatalf("anchors = %v, unverifiable = %v, want both empty: a comment is not code", anchors, unverifiable)
	}
}

func TestFindDocAnchorsSkipsTestFiles(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a_test.go": `package pkg

import "github.com/JoottunAtish/ShellForge/internal/platform/ux"

func f() error {
	return ux.Fail("op", nil, "remediation", "test-file-anchor")
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(anchors) != 0 || len(unverifiable) != 0 {
		t.Fatalf("anchors = %v, unverifiable = %v, want both empty: a _test.go call site is not learner-reachable", anchors, unverifiable)
	}
}

func TestHeadingSetHasAnchor(t *testing.T) {
	cases := []struct {
		name    string
		heading string
		anchor  string
		wantHas bool
	}{
		{"exact match", "## docker-daemon-down", "docker-daemon-down", true},
		{"anchor is a substring of a longer heading", "## docker-daemon-down-extra", "docker-daemon-down", true},
		{"no match", "## something-else", "docker-daemon-down", false},
		{"heading longer than anchor in the other direction is not a match", "## db", "progress-db-corrupt", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &headingSet{headings: []string{tc.heading}}
			if got := h.hasAnchor(tc.anchor); got != tc.wantHas {
				t.Errorf("hasAnchor(%q) against %q = %v, want %v", tc.anchor, tc.heading, got, tc.wantHas)
			}
		})
	}
}
