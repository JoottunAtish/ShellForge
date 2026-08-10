package runtime_test

// This file inspects the source of internal/runtime directly with go/ast,
// rather than through reflect, because reflect cannot answer questions about
// declaration order or package-level documentation. Two tests here,
// TestEveryExportedIdentifierIsDocumented and
// TestPackageImportsOnlyTheStandardLibrary, are honestly vacuous on an empty
// package: with zero exported identifiers and zero imports they pass with
// nothing to check. They become meaningful the moment the runtime package
// gains declarations, and they are the guard against those declarations
// drifting later.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
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

	t.Run("package exports no functions", func(t *testing.T) {
		// The package is types only. A convenience function such as ExecString,
		// RunShell, or MustExec would show up here.
		for _, f := range files {
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Recv != nil {
					continue
				}
				if fd.Name.IsExported() {
					t.Errorf("package runtime exports function %s; this package is types only", fd.Name.Name)
				}
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

		// Every interface method parameter.
		for _, ifaceName := range []string{"Runtime", "Session", "PTY"} {
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
				t.Fatalf("%s %s has no doc comment", d.kind, d.name)
			}
		})
	}
}

// TestDestructiveFieldsCarryTheirWarning is the machine-checkable form of the
// ticket's safety review. ImageSpec.Name and every User field will
// eventually be interpolated into a docker or wsl.exe argv, and the doc
// comment is what tells the next implementer to allowlist-validate rather
// than sanitize, and to compare a destroy target against a compile-time
// constant rather than trusting the field.
func TestDestructiveFieldsCarryTheirWarning(t *testing.T) {
	_, files := parsePackage(t)

	tests := []struct {
		typeName    string
		field       string
		mustContain []string
	}{
		{typeName: "ImageSpec", field: "Name", mustContain: []string{"^[a-zA-Z0-9_-]+$", "compile-time constant"}},
		{typeName: "SessionSpec", field: "User", mustContain: []string{"^[a-zA-Z0-9_-]+$"}},
		{typeName: "ExecOpts", field: "User", mustContain: []string{"^[a-zA-Z0-9_-]+$"}},
		{typeName: "AttachOpts", field: "User", mustContain: []string{"^[a-zA-Z0-9_-]+$"}},
	}

	for _, tt := range tests {
		t.Run(tt.typeName+"."+tt.field, func(t *testing.T) {
			st := findStruct(files, tt.typeName)
			if st == nil {
				t.Fatalf("type %s not declared in package runtime", tt.typeName)
			}
			var text string
			found := false
			for _, field := range st.Fields.List {
				for _, n := range field.Names {
					if n.Name != tt.field {
						continue
					}
					found = true
					text = docText(field.Doc) + " " + docText(field.Comment)
				}
			}
			if !found {
				t.Fatalf("field %s.%s not declared", tt.typeName, tt.field)
			}
			for _, want := range tt.mustContain {
				if !strings.Contains(text, want) {
					t.Errorf("%s.%s doc comment is missing %q; got %q", tt.typeName, tt.field, want, text)
				}
			}
		})
	}
}

// TestPackageImportsOnlyTheStandardLibrary rejects any import whose first
// path segment contains a dot, which covers both a third-party module and
// this module's own path, proving the package has no intra-module
// dependency either.
func TestPackageImportsOnlyTheStandardLibrary(t *testing.T) {
	fset, files := parsePackage(t)

	for _, f := range files {
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			first := strings.SplitN(path, "/", 2)[0]
			if strings.Contains(first, ".") {
				t.Errorf("internal/runtime imports %q in %s; this package is types only and must build "+
					"against the standard library alone", path, fset.Position(imp.Pos()).Filename)
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
