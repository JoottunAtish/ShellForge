package verify

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// coveredTypes is populated by each *_checks_test.go file's init, one call
// to markCovered per check type it exercises. TestAllRegisteredTypesHaveCoverage
// in registry_test.go asserts every name Types() returns appears here, so a
// fourteenth check type registered without a table test fails the build
// rather than shipping silently untested.
var coveredTypes = map[string]bool{}

func markCovered(types ...string) {
	for _, t := range types {
		coveredTypes[t] = true
	}
}

func TestParamStringRequired(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		wantErr bool
		want    string
	}{
		{"present", map[string]any{"path": "/a"}, false, "/a"},
		{"missing", map[string]any{}, true, ""},
		{"empty", map[string]any{"path": ""}, true, ""},
		{"wrong type", map[string]any{"path": 5}, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := paramString(tt.params, "path", true)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParamStringOptional(t *testing.T) {
	got, err := paramString(map[string]any{}, "path", false)
	if err != nil || got != "" {
		t.Fatalf("optional missing param: got (%q, %v), want (\"\", nil)", got, err)
	}
}

func TestParamStringPresence(t *testing.T) {
	tests := []struct {
		name        string
		params      map[string]any
		wantValue   string
		wantPresent bool
		wantErr     bool
	}{
		{"absent", map[string]any{}, "", false, false},
		{"present empty", map[string]any{"value": ""}, "", true, false},
		{"present non-empty", map[string]any{"value": "x"}, "x", true, false},
		{"wrong type", map[string]any{"value": 5}, "", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, present, err := paramStringPresence(tt.params, "value")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if present != tt.wantPresent || (!tt.wantErr && v != tt.wantValue) {
				t.Fatalf("got (%q, %v), want (%q, %v)", v, present, tt.wantValue, tt.wantPresent)
			}
		})
	}
}

func TestParamBool(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		want    bool
		wantErr bool
	}{
		{"missing", map[string]any{}, false, false},
		{"true", map[string]any{"negate": true}, true, false},
		{"wrong type", map[string]any{"negate": "true"}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := paramBool(tt.params, "negate")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParamStringSlice(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		want    []string
		wantErr bool
	}{
		{"missing", map[string]any{}, nil, false},
		{"any slice", map[string]any{"any_of": []any{"0700", "0750"}}, []string{"0700", "0750"}, false},
		{"string slice", map[string]any{"any_of": []string{"0700"}}, []string{"0700"}, false},
		{"wrong element type", map[string]any{"any_of": []any{1}}, nil, true},
		{"wrong type entirely", map[string]any{"any_of": "0700"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := paramStringSlice(tt.params, "any_of")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// --- import-freedom source-parsing test ---
//
// Mirrors internal/runtime/runtimetest's TestPackageImportsNoBackend: parse
// every non-test .go file in this package's directory and fail if any of
// them imports internal/journal. internal/archtest's layer test would not
// catch this on its own: internal/journal is L2 and internal/verify is L3,
// so importing it is a legal downward edge by the layer rule alone. This
// test enforces the additional, package-specific contract that doc.go
// states: internal/verify never imports internal/journal at all, because it
// declares its own JournalReader interface instead.

const journalImportPath = "github.com/JoottunAtish/ShellForge/internal/journal"

func TestPackageDoesNotImportJournal(t *testing.T) {
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to report this file's path")
	}
	dir := filepath.Dir(thisFile)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		for _, imp := range f.Imports {
			p := importPath(imp)
			if p == journalImportPath {
				t.Errorf("%s imports %q: internal/verify must declare its own JournalReader interface instead, see doc.go", name, p)
			}
		}
	}
}

func importPath(imp *ast.ImportSpec) string {
	v := imp.Path.Value
	return strings.Trim(v, `"`)
}
