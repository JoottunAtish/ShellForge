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
		fwd := findForwarders(files, consts)

		for _, f := range files {
			if !f.uxBind.reachable() && len(fwd) == 0 {
				// This file cannot contain a real ux.Fail or ux.Error site,
				// and its package has no forwarder for it to call either.
				continue
			}
			// Walked one declaration at a time so that a forwarder's own
			// body can be told apart from everywhere else. The anchor a
			// forwarder hands to ux.Fail is its caller's, not its own, and
			// is checked at the call site that supplied it.
			for _, decl := range f.ast.Decls {
				forwarded := forwardedParamOf(decl, fwd)

				ast.Inspect(decl, func(n ast.Node) bool {
					expr, ok := anchorSite(n, f.uxBind, fwd)
					if !ok {
						return true
					}
					if expr == nil {
						// A recognised Fail(...) call, Error{} literal or
						// forwarder call whose anchor argument this package
						// could not even locate, e.g. ux.Fail(spreadArgs()...).
						// Reported rather than silently passed over, for the
						// same reason any other unresolvable anchor is: a
						// silently skipped anchor is exactly how the grep
						// this package replaces went blind.
						unverifiable = append(unverifiable, position(fset, n.Pos()))
						return true
					}
					value, resolvable := resolveString(expr, consts)
					if !resolvable {
						if forwarded != "" && isIdentNamed(expr, forwarded) {
							// The plumbing inside a forwarder: this anchor
							// arrives as a parameter and is checked at every
							// call site of the forwarder instead.
							return true
						}
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
// forwarder is a function that carries no doc anchor of its own: it takes
// one from its caller and hands it to ux.Fail. cmd/shellforge's
// failUnlessAlreadyUserFacing(op, err, remediation, docAnchor) is the one in
// this repository today.
//
// Such a function is two things at once and both have to be handled or the
// gate is wrong in one direction or the other. Its own ux.Fail call reads
// the anchor out of a parameter, which no static analysis can resolve, so
// reporting it as unverifiable is a false positive. Its CALL SITES are where
// a real anchor is written down, so not checking them is a blind spot, and
// exactly the blind spot converting a ux.Fail call into a forwarder would
// silently open.
type forwarder struct {
	param    string // the parameter the anchor arrives under
	argIndex int    // that parameter's zero-based position in the argument list
}

// forwarderSet holds the forwarders of one package, keyed by function name.
//
// Package scoped because it is discovered from the package's own source and
// consumed there: see findForwarders on why a forwarder has to be
// unexported to be recognised at all.
type forwarderSet map[string]forwarder

// lookup reports whether fun calls a forwarder in this set.
//
// Only the bare identifier form is matched, which is the only way to spell a
// call to an unexported function of your own package.
func (s forwarderSet) lookup(fun ast.Expr) (forwarder, bool) {
	ident, ok := fun.(*ast.Ident)
	if !ok {
		return forwarder{}, false
	}
	f, found := s[ident.Name]
	return f, found
}

// findForwarders discovers the forwarders declared across one package's
// files.
//
// This is deliberately discovery rather than a registry. An earlier gate in
// cmd/shellforge kept a hand-maintained `anchorForwarders` map, which works
// right up to the moment somebody writes a second forwarder and does not
// know the map exists. A rule enforced by a list somebody has to remember to
// extend decays; see issue #132.
//
// Three shapes are deliberately NOT recognised, and each fails closed and
// loudly rather than quietly:
//
//   - An exported function. A forwarder is followed only within its own
//     package, so an exported one could be called from a package this walk
//     would never connect to it, and its call sites would go unchecked in
//     silence. Left unrecognised, its internal ux.Fail is reported as
//     unverifiable instead, which says so out loud.
//   - A method. Same reason: the call site is spelled through a receiver
//     whose type this walk does not resolve.
//   - A parameter that is also the name of a package-level string constant.
//     resolveString would resolve the constant, so which of the two a reader
//     means is ambiguous, and an ambiguous exemption is not one worth having.
//
// The loop runs to a fixed point so a forwarder that calls another forwarder
// is recognised too, whichever order the files were parsed in.
func findForwarders(files []parsedFile, consts map[string]string) forwarderSet {
	found := forwarderSet{}
	for {
		grew := false
		for _, f := range files {
			for _, decl := range f.ast.Decls {
				fn, isFunc := decl.(*ast.FuncDecl)
				if !isFunc || fn.Recv != nil || fn.Name == nil || fn.Body == nil {
					continue
				}
				if ast.IsExported(fn.Name.Name) {
					continue
				}
				if _, already := found[fn.Name.Name]; already {
					continue
				}
				fw, ok := forwardedParam(fn, f.uxBind, found, consts)
				if !ok {
					continue
				}
				found[fn.Name.Name] = fw
				grew = true
			}
		}
		if !grew {
			return found
		}
	}
}

// forwardedParam reports which of fn's own string parameters it hands on as
// a doc anchor, if any.
func forwardedParam(fn *ast.FuncDecl, bind uxBinding, known forwarderSet, consts map[string]string) (forwarder, bool) {
	params := stringParams(fn.Type.Params)
	if len(params) == 0 {
		return forwarder{}, false
	}

	var result forwarder
	ok := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if ok {
			return false
		}
		expr, isSite := anchorSite(n, bind, known)
		if !isSite || expr == nil {
			return true
		}
		ident, isIdent := expr.(*ast.Ident)
		if !isIdent {
			return true
		}
		if _, shadowsAConst := consts[ident.Name]; shadowsAConst {
			return true
		}
		index, isParam := params[ident.Name]
		if !isParam {
			return true
		}
		result = forwarder{param: ident.Name, argIndex: index}
		ok = true
		return false
	})
	return result, ok
}

// stringParams maps the name of every string-typed parameter to its
// zero-based position in the argument list, flattening a grouped
// declaration: in (op string, err error, remediation, docAnchor string),
// docAnchor is index 3.
//
// Only the bare `string` type is matched. A named string type would still
// carry an anchor, but nothing in this repository declares one, and matching
// it would mean resolving type names across packages for a case that does
// not exist.
func stringParams(params *ast.FieldList) map[string]int {
	if params == nil {
		return nil
	}
	out := map[string]int{}
	index := 0
	for _, field := range params.List {
		ident, isIdent := field.Type.(*ast.Ident)
		isString := isIdent && ident.Name == "string"
		if len(field.Names) == 0 {
			index++ // an unnamed parameter still occupies a position
			continue
		}
		for _, name := range field.Names {
			if isString && name.Name != "_" {
				out[name.Name] = index
			}
			index++
		}
	}
	return out
}

// forwardedParamOf reports the parameter decl forwards, when decl is one of
// the package's forwarders, and the empty string otherwise.
func forwardedParamOf(decl ast.Decl, fwd forwarderSet) string {
	fn, isFunc := decl.(*ast.FuncDecl)
	if !isFunc || fn.Name == nil {
		return ""
	}
	f, isForwarder := fwd[fn.Name.Name]
	if !isForwarder {
		return ""
	}
	return f.param
}

// isIdentNamed reports whether expr is exactly the identifier name.
func isIdentNamed(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

// anchorSite returns the doc anchor argument of n, whether n is a direct
// ux.Fail call, a ux.Error composite literal, or a call to one of this
// package's forwarders. A forwarder call site is checked exactly as a
// ux.Fail is, because that is what it is.
//
// ok reports that n is a recognised site at all. A nil expr with ok true
// means the site was recognised but its anchor argument could not be
// located, which is reported rather than skipped.
func anchorSite(n ast.Node, pkg uxBinding, fwd forwarderSet) (expr ast.Expr, ok bool) {
	if call, isCall := n.(*ast.CallExpr); isCall {
		if f, isForwarder := fwd.lookup(call.Fun); isForwarder {
			if len(call.Args) <= f.argIndex {
				return nil, true
			}
			return call.Args[f.argIndex], true
		}
	}
	return docAnchorArg(n, pkg)
}

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

// TestFindDocAnchorsUnresolvableIsReported uses a computed anchor rather
// than a parameter on purpose. A parameter handed straight to ux.Fail is a
// forwarder, which has its own tests below; a value returned by a function
// is the shape nothing can follow and nothing should pretend to.
func TestFindDocAnchorsUnresolvableIsReported(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a.go": `package pkg

import "github.com/JoottunAtish/ShellForge/internal/platform/ux"

func pick() string { return "computed" }

func f() error {
	return ux.Fail("op", nil, "remediation", pick())
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(anchors) != 0 {
		t.Fatalf("anchors = %v, want none: a call result is not a resolvable anchor", anchors)
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

// --------------------------------------------------------------------------
// Forwarders. A helper that takes (op, err, remediation, docAnchor) and
// hands them to ux.Fail is not an anchor site itself: its call sites are.
// See issue #132, and cmd/shellforge's failUnlessAlreadyUserFacing.
// --------------------------------------------------------------------------

// forwarderFixture is the shape this repository actually ships, minus the
// error handling: an unexported helper whose fourth parameter is the anchor.
const forwarderFixture = `package pkg

import "github.com/JoottunAtish/ShellForge/internal/platform/ux"

func failUnlessAlreadyUserFacing(op string, err error, remediation, docAnchor string) error {
	if err == nil {
		return nil
	}
	return ux.Fail(op, err, remediation, docAnchor)
}
`

func TestFindDocAnchorsFollowsAForwarderToItsCallSite(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/forward.go": forwarderFixture,
		"pkg/call.go": `package pkg

func g(err error) error {
	return failUnlessAlreadyUserFacing("op", err, "remediation", "real-anchor")
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(unverifiable) != 0 {
		t.Errorf("unverifiable = %v, want none: the forwarder's own ux.Fail reads its caller's anchor", unverifiable)
	}
	if len(anchors) != 1 || anchors[0] != "real-anchor" {
		t.Errorf("anchors = %v, want [real-anchor] from the call site", anchors)
	}
}

// TestFindDocAnchorsChecksAForwarderCallSiteItCannotRead is the half that
// matters most: following a forwarder must not become a way to launder an
// unreadable anchor past the gate.
func TestFindDocAnchorsChecksAForwarderCallSiteItCannotRead(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/forward.go": forwarderFixture,
		"pkg/call.go": `package pkg

func pick() string { return "computed" }

func g(err error) error {
	return failUnlessAlreadyUserFacing("op", err, "remediation", pick())
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(anchors) != 0 {
		t.Errorf("anchors = %v, want none", anchors)
	}
	if len(unverifiable) != 1 {
		t.Fatalf("unverifiable = %v, want exactly one entry naming the forwarder's call site", unverifiable)
	}
	if !strings.Contains(unverifiable[0], "call.go") {
		t.Errorf("unverifiable = %v, want the CALL SITE reported, not the forwarder's own ux.Fail", unverifiable)
	}
}

func TestFindDocAnchorsResolvesAConstAtAForwarderCallSite(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/forward.go": forwarderFixture,
		"pkg/call.go": `package pkg

const docAnchorThing = "const-anchor"

func g(err error) error {
	return failUnlessAlreadyUserFacing("op", err, "remediation", docAnchorThing)
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(unverifiable) != 0 {
		t.Errorf("unverifiable = %v, want none", unverifiable)
	}
	if len(anchors) != 1 || anchors[0] != "const-anchor" {
		t.Errorf("anchors = %v, want [const-anchor]", anchors)
	}
}

func TestFindDocAnchorsReportsAForwarderCallWithTooFewArguments(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/forward.go": forwarderFixture,
		"pkg/call.go": `package pkg

func parts() (string, error, string, string) { return "op", nil, "rem", "a" }

func g() error {
	return failUnlessAlreadyUserFacing(parts())
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(anchors) != 0 {
		t.Errorf("anchors = %v, want none", anchors)
	}
	if len(unverifiable) != 1 {
		t.Errorf("unverifiable = %v, want exactly one entry: a spread call hides its anchor", unverifiable)
	}
}

// TestFindDocAnchorsDoesNotFollowAnExportedForwarder pins the fail-closed
// choice. An exported helper can be called from a package this walk never
// connects to it, so it is not recognised as a forwarder and its own
// ux.Fail is reported instead. Loud beats a silent blind spot.
func TestFindDocAnchorsDoesNotFollowAnExportedForwarder(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a.go": `package pkg

import "github.com/JoottunAtish/ShellForge/internal/platform/ux"

func FailUnlessAlreadyUserFacing(op string, err error, remediation, docAnchor string) error {
	return ux.Fail(op, err, remediation, docAnchor)
}

func g(err error) error {
	return FailUnlessAlreadyUserFacing("op", err, "remediation", "real-anchor")
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(unverifiable) != 1 {
		t.Errorf("unverifiable = %v, want exactly one: an exported forwarder is not followed", unverifiable)
	}
	if len(anchors) != 0 {
		t.Errorf("anchors = %v, want none: the call site of an unrecognised forwarder is not an anchor site", anchors)
	}
}

// TestFindDocAnchorsDoesNotFollowAMethod is the same fail-closed choice for
// the other shape this walk cannot resolve a call site for.
func TestFindDocAnchorsDoesNotFollowAMethod(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a.go": `package pkg

import "github.com/JoottunAtish/ShellForge/internal/platform/ux"

type t struct{}

func (t) fail(op string, err error, remediation, docAnchor string) error {
	return ux.Fail(op, err, remediation, docAnchor)
}
`,
	})
	_, unverifiable := findDocAnchors(t, root)
	if len(unverifiable) != 1 {
		t.Errorf("unverifiable = %v, want exactly one: a method is not followed", unverifiable)
	}
}

// TestFindDocAnchorsFollowsAChainOfForwarders proves the fixed point. The
// outer forwarder never mentions ux at all, so it is only recognisable once
// the inner one is, whichever order the files were parsed in.
func TestFindDocAnchorsFollowsAChainOfForwarders(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/outer.go": `package pkg

func wrap(op string, err error, remediation, docAnchor string) error {
	return failUnlessAlreadyUserFacing(op, err, remediation, docAnchor)
}

func g(err error) error {
	return wrap("op", err, "remediation", "chained-anchor")
}
`,
		"pkg/forward.go": forwarderFixture,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(unverifiable) != 0 {
		t.Errorf("unverifiable = %v, want none", unverifiable)
	}
	if len(anchors) != 1 || anchors[0] != "chained-anchor" {
		t.Errorf("anchors = %v, want [chained-anchor]", anchors)
	}
}

// TestFindDocAnchorsPrefersAConstOverAShadowingParameter keeps the
// exemption unambiguous. A parameter sharing a package constant's name is
// not treated as a forwarder: resolveString would resolve the constant, and
// an exemption nobody can predict is worse than no exemption.
func TestFindDocAnchorsPrefersAConstOverAShadowingParameter(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"pkg/a.go": `package pkg

import "github.com/JoottunAtish/ShellForge/internal/platform/ux"

const docAnchor = "const-anchor"

func f(op string, err error, remediation, docAnchor string) error {
	return ux.Fail(op, err, remediation, docAnchor)
}
`,
	})
	anchors, unverifiable := findDocAnchors(t, root)
	if len(unverifiable) != 0 {
		t.Errorf("unverifiable = %v, want none: the name resolves as a package constant", unverifiable)
	}
	if len(anchors) != 1 || anchors[0] != "const-anchor" {
		t.Errorf("anchors = %v, want [const-anchor]", anchors)
	}
}

func TestStringParamsFlattensAGroupedDeclaration(t *testing.T) {
	src := `package pkg

func f(op string, err error, remediation, docAnchor string) {}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "a.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fn := file.Decls[0].(*ast.FuncDecl)

	got := stringParams(fn.Type.Params)
	want := map[string]int{"op": 0, "remediation": 2, "docAnchor": 3}
	if len(got) != len(want) {
		t.Fatalf("stringParams = %v, want %v", got, want)
	}
	for name, index := range want {
		if got[name] != index {
			t.Errorf("stringParams[%q] = %d, want %d", name, got[name], index)
		}
	}
	if _, present := got["err"]; present {
		t.Error("stringParams included err, which is not a string parameter")
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
