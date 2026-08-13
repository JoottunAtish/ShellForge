package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// modulePath must match the module line in go.mod.
const modulePath = "github.com/JoottunAtish/ShellForge"

// layers maps a package directory, relative to the module root and in slash
// form, to its layer number. A package may import its own layer and any layer
// below it. It may never import a layer above it.
var layers = map[string]int{
	"cmd/shellforge": 5,

	"internal/game": 4,

	"internal/content":           3,
	"internal/content/setup":     3,
	"internal/verify":            3,
	"internal/verify/verifytest": 3,

	"internal/pty":     2,
	"internal/journal": 2,

	"internal/runtime": 1,
	"internal/sandbox": 1,

	// Runtime backends. Registered ahead of Day 1 so adding them does not trip
	// the "no layer assigned" check. Rule B below is what actually confines
	// them: nothing above L1 may import these.
	"internal/runtime/docker":      1,
	"internal/runtime/wsl":         1,
	"internal/runtime/runtimetest": 1,

	"internal/store":       0,
	"internal/platform":    0,
	"internal/platform/ux": 0,
	"internal/doctor":      0,

	// The shipped content packs, embedded into the binary. It is data, not
	// a layer: one embed.FS, one string constant, no behaviour, and no
	// import but embed itself. It sits at the bottom so that internal/content
	// may read it without the dependency pointing upward. It exists as a Go
	// package at all only because embed cannot reach outside the directory
	// its directive is written in, so the loader in internal/content cannot
	// embed packs/core-linux-basics from three directories away.
	"packs": 0,

	// The test itself sits outside the layering.
	"internal/archtest": 99,
}

// runtimeImplPrefix marks the runtime backend implementations. Only the
// packages in runtimeImplAllowed may import them, so that no game or content
// code can grow a dependency on Docker or WSL specifics.
const runtimeImplPrefix = "internal/runtime/"

var runtimeImplAllowed = map[string]bool{
	"cmd/shellforge":    true,
	"internal/runtime":  true,
	"internal/sandbox":  true,
	"internal/archtest": true,
}

func TestLayerDependencies(t *testing.T) {
	root := moduleRoot(t)

	imports := collectImports(t, root)
	if len(imports) == 0 {
		t.Fatal("no Go packages found; the walk or the module root is wrong")
	}

	pkgs := make([]string, 0, len(imports))
	for pkg := range imports {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)

	for _, pkg := range pkgs {
		fromLayer, known := layers[pkg]
		if !known {
			t.Errorf("package %q has no layer assigned.\n"+
				"Add it to the layers table in internal/archtest/layers_test.go "+
				"and say in the commit message which layer it belongs to.", pkg)
			continue
		}

		for _, imp := range imports[pkg] {
			target := strings.TrimPrefix(imp.path, modulePath+"/")

			// Rule A: no upward edges.
			toLayer, known := layers[target]
			if !known {
				// An unlisted internal package is itself a failure, reported
				// when we reach it in the outer loop. Skip silently here.
				if nearest, ok := nearestKnown(target); ok {
					toLayer = nearest
				} else {
					continue
				}
			}
			if fromLayer != 99 && toLayer > fromLayer {
				t.Errorf("layer violation in %s\n"+
					"  %s (L%d) imports %s (L%d)\n"+
					"  Dependencies point downward only. If %s needs information from "+
					"the higher layer, pass it in or expose it through an interface.",
					imp.file, pkg, fromLayer, target, toLayer, pkg)
			}

			// Rule B: runtime implementations are off limits above L1.
			if strings.HasPrefix(target, runtimeImplPrefix) && !runtimeImplAllowed[pkg] {
				t.Errorf("runtime implementation leak in %s\n"+
					"  %s imports %s\n"+
					"  Only the Runtime interface may be used above L1. If you need "+
					"backend information, add it to Runtime.Capabilities().",
					imp.file, pkg, target)
			}
		}
	}
}

// TestNoNetworkFromGameLayer guards a product promise: Shellforge has no
// telemetry and works fully offline after install, so no HTTP client should be
// reachable from the content, verification, or game layers.
//
// This is a coarse check and deliberately so. It catches the accidental import
// long before anyone writes a request.
func TestNoNetworkFromGameLayer(t *testing.T) {
	root := moduleRoot(t)
	imports := collectImports(t, root)

	banned := map[string]string{
		"net/http": "the game layer must not make network calls; Shellforge has no telemetry and works offline",
	}

	for pkg, imps := range imports {
		if layers[pkg] < 3 || layers[pkg] == 99 {
			continue
		}
		for _, imp := range imps {
			if why, bad := banned[imp.path]; bad {
				t.Errorf("%s imports %s in %s\n  %s", pkg, imp.path, imp.file, why)
			}
		}
	}
}

type importRef struct {
	path string
	file string
}

// collectImports returns, for every package directory in the module, the module
// -internal and standard library imports of its non-test Go files.
func collectImports(t *testing.T, root string) map[string][]importRef {
	t.Helper()

	result := make(map[string][]importRef)
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

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		pkgDir := path.Dir(filepath.ToSlash(rel))

		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("parse %s: %v", rel, err)
			return nil
		}
		for _, spec := range f.Imports {
			ip, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			result[pkgDir] = append(result[pkgDir], importRef{path: ip, file: filepath.ToSlash(rel)})
		}
		// Record the package even when it imports nothing.
		if _, seen := result[pkgDir]; !seen {
			result[pkgDir] = nil
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}
	return result
}

// nearestKnown resolves a subpackage such as internal/runtime/docker to the
// layer of its nearest listed ancestor.
func nearestKnown(pkg string) (int, bool) {
	for p := pkg; p != "." && p != "/" && p != ""; p = path.Dir(p) {
		if l, ok := layers[p]; ok {
			return l, true
		}
	}
	return 0, false
}

// moduleRoot walks up from the test's directory until it finds go.mod.
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
