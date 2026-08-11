package runtime_test

// This file inspects the source of internal/runtime directly with go/ast,
// rather than through reflect, because reflect cannot answer questions about
// declaration order or package-level documentation: it sorts interface
// methods alphabetically, it cannot see whether an identifier has a doc
// comment at all, and it cannot see an import that nothing in the compiled
// program still references. go/ast sees the source as written, which is
// exactly what these tests need to check.
//
// TestEveryExportedIdentifierIsDocumented walks every exported top-level
// type, top-level value, interface method, and exported struct field the
// package now declares (ten types, three sentinel errors, and roughly forty
// exported fields) and fails if any of them lacks a doc comment starting
// with its own name. TestPackageImportsOnlyTheStandardLibrary walks every
// import in every non-test file and fails on anything outside the standard
// library, with two named exceptions the package doc comment and the ticket
// both call out. Neither test is vacuous: both have real declarations to
// check, and both are exercised by deliberately breaking a declaration and
// confirming the test catches it.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// parsePackage parses the non-test Go files of this directory with comments
// attached, mirroring the walk in internal/archtest/layers_test.go. It avoids
// the deprecated parser.ParseDir.
func parsePackage(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read internal/runtime: %v", err)
	}

	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no non-test Go files found in internal/runtime")
	}
	return fset, files
}

// findInterface returns the *ast.InterfaceType named iface, declared at the
// top level of one of files.
func findInterface(files []*ast.File, iface string) *ast.InterfaceType {
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != iface {
					continue
				}
				it, ok := ts.Type.(*ast.InterfaceType)
				if !ok {
					return nil
				}
				return it
			}
		}
	}
	return nil
}

// topLevelInterfaceNames returns the name of every interface type declared at
// the top level of files. Callers use this instead of a hardcoded list of
// interface names, so a new exported interface is covered by the same checks
// as Runtime, Session, and PTY without anyone remembering to add it to a
// list.
func topLevelInterfaceNames(files []*ast.File) []string {
	var names []string
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if _, ok := ts.Type.(*ast.InterfaceType); ok {
					names = append(names, ts.Name.Name)
				}
			}
		}
	}
	return names
}

// findStruct returns the *ast.StructType named typeName, declared at the top
// level of one of files.
func findStruct(files []*ast.File, typeName string) *ast.StructType {
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != typeName {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return nil
				}
				return st
			}
		}
	}
	return nil
}

// TestInterfaceMethodOrder asserts Runtime, Session, and PTY declare their
// methods in exactly the order the ticket specifies. reflect.Type sorts
// methods alphabetically and cannot see declaration order, so this walks the
// AST instead. Moving one method above another fails only here.
func TestInterfaceMethodOrder(t *testing.T) {
	_, files := parsePackage(t)

	tests := []struct {
		iface string
		want  []string
	}{
		{iface: "Runtime", want: []string{"Provision", "Destroy", "Status", "StartSession", "Capabilities"}},
		{iface: "Session", want: []string{"Exec", "Attach", "PushFiles", "PullFile", "Close"}},
		{iface: "PTY", want: []string{"io.ReadWriteCloser", "Resize", "Wait"}},
	}

	for _, tt := range tests {
		t.Run(tt.iface, func(t *testing.T) {
			it := findInterface(files, tt.iface)
			if it == nil {
				t.Fatalf("interface %s not declared in package runtime", tt.iface)
			}

			var got []string
			for _, field := range it.Methods.List {
				if len(field.Names) == 1 {
					got = append(got, field.Names[0].Name)
					continue
				}
				got = append(got, types.ExprString(field.Type))
			}

			if len(got) != len(tt.want) {
				t.Fatalf("interface %s has methods %v, want %v", tt.iface, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("interface %s method %d is %q, want %q (full order: got %v, want %v)",
						tt.iface, i, got[i], tt.want[i], got, tt.want)
				}
			}
		})
	}
}

// TestExecTakesArgvNotACommandString is the machine-checkable form of the
// security boundary in the security skill: Exec takes argv []string, never a
// command string, and there is no overload or helper anywhere in the package
// that accepts one.
func TestExecTakesArgvNotACommandString(t *testing.T) {
	_, files := parsePackage(t)

	t.Run("argv is a slice", func(t *testing.T) {
		it := findInterface(files, "Session")
		if it == nil {
			t.Fatal("interface Session not declared in package runtime")
		}
		var exec *ast.Field
		for _, field := range it.Methods.List {
			if len(field.Names) == 1 && field.Names[0].Name == "Exec" {
				exec = field
			}
		}
		if exec == nil {
			t.Fatal("Session.Exec not declared")
		}
		ft, ok := exec.Type.(*ast.FuncType)
		if !ok {
			t.Fatal("Session.Exec is not a function type")
		}
		if ft.Params.NumFields() < 2 {
			t.Fatalf("Session.Exec has %d parameters, want at least 2", ft.Params.NumFields())
		}
		argv := ft.Params.List[1]
		if _, ok := argv.Type.(*ast.ArrayType); !ok {
			t.Fatalf("Session.Exec parameter 1 is %s, want []string", types.ExprString(argv.Type))
		}
		if got := types.ExprString(argv.Type); got != "[]string" {
			t.Fatalf("Session.Exec parameter 1 is %s, want []string", got)
		}
	})

	t.Run("package exports no functions or methods", func(t *testing.T) {
		// The package is types only: no behaviour, so no exported function
		// AND no exported method either, regardless of receiver. A
		// convenience function such as ExecString or RunShell would show up
		// here, and so would a convenience method such as
		// func (o ExecOpts) ExecString(cmd string) []string, which a
		// receiver-blind check would have missed entirely.
		for _, f := range files {
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || !fd.Name.IsExported() {
					continue
				}
				kind := "function"
				if fd.Recv != nil {
					kind = "method"
				}
				t.Errorf("package runtime exports %s %s; this package is types only and declares no behaviour",
					kind, fd.Name.Name)
			}
		}
	})

	t.Run("no command-string parameter or field", func(t *testing.T) {
		banned := []string{"cmd", "cmdline", "command", "commandline", "script", "shell", "sh", "line"}
		isBanned := func(name string) bool {
			lower := strings.ToLower(name)
			for _, b := range banned {
				if lower == b {
					return true
				}
			}
			return false
		}

		// Every interface method parameter, on every interface the package
		// declares, not a hardcoded list of the three known when this test
		// was written. A new exported interface with a string parameter
		// named like a command, such as Run(ctx context.Context, command
		// string), is covered automatically.
		for _, ifaceName := range topLevelInterfaceNames(files) {
			it := findInterface(files, ifaceName)
			if it == nil {
				continue
			}
			for _, field := range it.Methods.List {
				ft, ok := field.Type.(*ast.FuncType)
				if !ok || ft.Params == nil {
					continue
				}
				for _, p := range ft.Params.List {
					if types.ExprString(p.Type) != "string" {
						continue
					}
					for _, n := range p.Names {
						if isBanned(n.Name) {
							methodName := ""
							if len(field.Names) == 1 {
								methodName = field.Names[0].Name
							}
							t.Errorf("parameter %q on %s.%s is a bare string named like a command; "+
								"Exec takes argv []string and there is no string form anywhere in this package",
								n.Name, ifaceName, methodName)
						}
					}
				}
			}
		}

		// Every exported struct field.
		for _, f := range files {
			for _, decl := range f.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.TYPE {
					continue
				}
				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					for _, field := range st.Fields.List {
						if types.ExprString(field.Type) != "string" {
							continue
						}
						for _, n := range field.Names {
							if n.IsExported() && isBanned(n.Name) {
								t.Errorf("field %s.%s is a bare string named like a command; "+
									"Exec takes argv []string and there is no string form anywhere in this package",
									ts.Name.Name, n.Name)
							}
						}
					}
				}
			}
		}
	})
}

// decl is one exported top-level identifier collected from the package
// source, along with its doc comment text.
type decl struct {
	kind string
	name string
	doc  string
}

// collectExportedDecls walks files and returns every exported top-level type,
// top-level value, interface method, and exported struct field, so that
// TestEveryExportedIdentifierIsDocumented cannot go stale against a
// hand-written list.
func collectExportedDecls(files []*ast.File) []decl {
	var decls []decl

	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if !s.Name.IsExported() {
						continue
					}
					doc := s.Doc
					if doc == nil {
						doc = gd.Doc
					}
					decls = append(decls, decl{kind: "type", name: s.Name.Name, doc: docText(doc)})

					switch t := s.Type.(type) {
					case *ast.InterfaceType:
						for _, field := range t.Methods.List {
							if len(field.Names) != 1 {
								continue
							}
							decls = append(decls, decl{
								kind: "method",
								name: s.Name.Name + "." + field.Names[0].Name,
								doc:  docText(field.Doc),
							})
						}
					case *ast.StructType:
						for _, field := range t.Fields.List {
							for _, n := range field.Names {
								if !n.IsExported() {
									continue
								}
								doc := field.Doc
								text := docText(doc)
								if text == "" {
									text = docText(field.Comment)
								}
								decls = append(decls, decl{
									kind: "field",
									name: s.Name.Name + "." + n.Name,
									doc:  text,
								})
							}
						}
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if !n.IsExported() {
							continue
						}
						doc := s.Doc
						if doc == nil {
							doc = gd.Doc
						}
						decls = append(decls, decl{kind: "value", name: n.Name, doc: docText(doc)})
					}
				}
			}
		}
	}

	return decls
}

func docText(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	return cg.Text()
}

// TestEveryExportedIdentifierIsDocumented asserts every exported top-level
// type, top-level value, interface method, and exported struct field carries
// a doc comment that is a full sentence beginning with the identifier's name,
// per the go-style skill.
func TestEveryExportedIdentifierIsDocumented(t *testing.T) {
	_, files := parsePackage(t)

	for _, d := range collectExportedDecls(files) {
		d := d
		t.Run(d.name, func(t *testing.T) {
			trimmed := strings.TrimSpace(d.doc)
			if trimmed == "" {
				t.Fatalf("%s %s has no doc comment", d.kind, d.name)
			}
			// A method or field doc comment is conventionally written against
			// the bare identifier, not the Type.Identifier form used to make
			// the failure message unambiguous above.
			bare := d.name
			if i := strings.LastIndex(bare, "."); i >= 0 {
				bare = bare[i+1:]
			}
			if !strings.HasPrefix(trimmed, bare) {
				t.Fatalf("%s %s has a doc comment that does not start with %q: got %q", d.kind, d.name, bare, trimmed)
			}
		})
	}
}

// allowlistPattern is the identifier allowlist regexp the security skill
// requires on every field that reaches a docker or wsl.exe argv. It forces
// the first character to be alphanumeric, so a validated value can never
// begin with a hyphen and be mistaken for a flag such as -f or --force.
// That is argument injection, a different failure from shell injection, and
// passing an argv vector instead of a command string does not close it.
//
// The looser single-class form this replaced, the one that allowed a hyphen
// in any position including the first, is deliberately not written out here.
// scripts/check-allowlist-regexp.sh fails the build if it appears anywhere in
// the tree, and a copy sitting inside a comment that explains why it is wrong
// would be indistinguishable from a copy somebody is about to trust. Issue
// #19 removed the last of them and .claude/skills/security/SKILL.md now
// quotes the same regexp this constant pins.
const allowlistPattern = `^[a-zA-Z0-9][a-zA-Z0-9_-]*$`

// TestAllowlistPatternRefusesFlagShapedValues turns the reason for the
// leading-character restriction into an executable assertion, so a future
// loosening of allowlistPattern goes red here rather than only in prose.
func TestAllowlistPatternRefusesFlagShapedValues(t *testing.T) {
	re, err := regexp.Compile(allowlistPattern)
	if err != nil {
		t.Fatalf("compile allowlistPattern %q: %v", allowlistPattern, err)
	}

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"plain name", "shellforge", true},
		{"sandbox user", "learner", true},
		{"single letter", "a", true},
		{"leading digit", "0abc", true},
		{"underscore and hyphen inside", "img_v2-1", true},
		{"pack id", "core-linux-basics", true},
		{"empty", "", false},
		{"short flag", "-f", false},
		{"long flag", "--force", false},
		{"bare hyphen", "-", false},
		{"leading underscore", "_tmp", false},
		{"embedded space", "a b", false},
		{"shell metacharacter", "a;b", false},
		{"path separator", "a/b", false},
		{"parent traversal", "..", false},
		{"trailing newline", "ok\n", false},
		{"embedded newline", "a\nb", false},
		{"non ascii", "caf\u00e9", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := re.MatchString(tt.value); got != tt.want {
				t.Errorf("allowlistPattern.MatchString(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// TestDestructiveFieldsCarryTheirWarning is the machine-checkable form of the
// ticket's safety review. ImageSpec.Name, every User field, and
// FileEntry.Path will eventually be interpolated into a docker or wsl.exe
// argv, or trusted by a materialize-and-later-reset path, and the doc
// comment is what tells the next implementer to allowlist-validate rather
// than sanitize, to compare a destroy target against a compile-time constant
// rather than trusting the field, and to refuse rather than adjust a path
// that resolves outside the level setup root.
//
// requiredSubstrings is keyed by field name, not by type-plus-field, and the
// walk below checks every exported field of every exported struct whose name
// appears in the map. That is deliberate: a hardcoded type-plus-field table
// only protects the four fields someone remembered to list, so a new
// exported struct that later adds its own field named Name, User, or Path
// arrives uncovered. Keying by name means it is covered the moment it is
// declared.
func TestDestructiveFieldsCarryTheirWarning(t *testing.T) {
	_, files := parsePackage(t)

	requiredSubstrings := map[string][]string{
		"Name": {allowlistPattern, "reject", "sanitiz", "destroy", "compile-time constant"},
		"User": {allowlistPattern},
		"Path": {"level setup root", "/home/learner/", "resolve", "symlink", "refus"},
	}

	seen := map[string]bool{}

	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range st.Fields.List {
					for _, n := range field.Names {
						if !n.IsExported() {
							continue
						}
						want, ok := requiredSubstrings[n.Name]
						if !ok {
							continue
						}
						key := ts.Name.Name + "." + n.Name
						seen[key] = true
						text := docText(field.Doc) + " " + docText(field.Comment)
						t.Run(key, func(t *testing.T) {
							for _, w := range want {
								if !strings.Contains(text, w) {
									t.Errorf("%s doc comment is missing %q; got %q", key, w, text)
								}
							}
						})
					}
				}
			}
		}
	}

	// The walk above is silent if it finds nothing to check. Pin the fields
	// the ticket actually names, so a rename or removal of one of them fails
	// here instead of the test quietly running zero subtests.
	for _, want := range []string{
		"ImageSpec.Name", "SessionSpec.User", "ExecOpts.User", "AttachOpts.User", "FileEntry.Path",
	} {
		if !seen[want] {
			t.Errorf("expected field %s while walking exported structs, but did not find it; "+
				"it may have been renamed or removed", want)
		}
	}
}

// allowedModuleImports are the only two module-internal import paths this
// package may use. doc.go and the ticket both say internal/platform and
// internal/platform/ux are permitted, so the test allowlists exactly those
// two paths before applying the dot check below. Without this, the dot check
// alone would reject them too, because this module's own import path also
// contains a dot, and the test would be asserting something the package doc
// comment explicitly contradicts.
var allowedModuleImports = map[string]bool{
	"github.com/JoottunAtish/ShellForge/internal/platform":    true,
	"github.com/JoottunAtish/ShellForge/internal/platform/ux": true,
}

// TestPackageImportsOnlyTheStandardLibrary rejects any import whose first
// path segment contains a dot, other than the two module-internal paths this
// package is explicitly allowed to use, which covers a third-party module
// and every other intra-module dependency.
func TestPackageImportsOnlyTheStandardLibrary(t *testing.T) {
	fset, files := parsePackage(t)

	for _, f := range files {
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if allowedModuleImports[path] {
				continue
			}
			first := strings.SplitN(path, "/", 2)[0]
			if strings.Contains(first, ".") {
				t.Errorf("internal/runtime imports %q in %s; this package must build against the standard "+
					"library plus only internal/platform and internal/platform/ux", path, fset.Position(imp.Pos()).Filename)
			}
		}
	}
}

// TestPackageDocShowsTheConformanceAssertion asserts doc.go's package comment
// documents the compile-time conformance pattern implementers are expected to
// use, per acceptance criterion 5.
func TestPackageDocShowsTheConformanceAssertion(t *testing.T) {
	_, files := parsePackage(t)

	var docComment string
	for _, f := range files {
		if f.Doc != nil && f.Name.Name == "runtime" {
			docComment = f.Doc.Text()
			break
		}
	}
	if docComment == "" {
		t.Fatal("package doc comment not found")
	}

	const want = "var _ Runtime = (*dockerRuntime)(nil)"
	if !strings.Contains(docComment, want) {
		t.Fatalf("package doc comment does not contain %q", want)
	}
}
