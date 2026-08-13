package archtest

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// confinedImports restricts an external dependency to exactly one package.
// goccy/go-yaml is the level YAML parser: only internal/content may reach for
// it, so a shortcut YAML read elsewhere in the engine can never bypass the
// validator that #53 builds on top of it. The sqlite driver is
// internal/store's alone for the same reason: nothing outside the
// persistence layer may open its own connection to the progress database.
var confinedImports = map[string]string{
	"github.com/goccy/go-yaml": "internal/content",
	"modernc.org/sqlite":       "internal/store",
}

// confinedModuleFor reports which package, if any, is the sole permitted
// importer of importPath.
//
// It matches a confined module's subpackages too, not only its root. The
// level loader legitimately imports github.com/goccy/go-yaml/ast to decode a
// check's line number, and an exact-match rule would have treated that as an
// unconfined third party import: the parser's own AST package would have been
// reachable from anywhere in the module while the parser itself was fenced,
// which is the fence with a door in it.
func confinedModuleFor(importPath string) (string, bool) {
	for module, owner := range confinedImports {
		if importPath == module || strings.HasPrefix(importPath, module+"/") {
			return owner, true
		}
	}
	return "", false
}

// TestConfinedDependenciesStayConfined enforces confinedImports. Verified
// against a deliberate violation before this test was committed: a temporary
// blank import of modernc.org/sqlite added to cmd/shellforge failed this
// test with the expected message, then was removed.
func TestConfinedDependenciesStayConfined(t *testing.T) {
	root := moduleRoot(t)
	imports := collectImports(t, root)

	for pkg, imps := range imports {
		for _, imp := range imps {
			owner, restricted := confinedModuleFor(imp.path)
			if !restricted || pkg == owner {
				continue
			}
			t.Errorf("layer violation in %s\n"+
				"  %s imports %s\n"+
				"  Only %s may import this dependency.",
				imp.file, pkg, imp.path, owner)
		}
	}
}

// goStyleSkillPath is the one place the approved dependency table lives, per
// CLAUDE.md's own "What NOT to do" section.
const goStyleSkillPath = ".claude/skills/go-style/SKILL.md"

var skillTableRow = regexp.MustCompile("^\\|\\s*`([^`]+)`\\s*\\|")

// approvedDependencies reads the Dependencies table out of the go-style
// skill and returns the set of module paths it lists.
func approvedDependencies(t *testing.T, root string) map[string]bool {
	t.Helper()

	f, err := os.Open(filepath.Join(root, goStyleSkillPath))
	if err != nil {
		t.Fatalf("open %s: %v", goStyleSkillPath, err)
	}
	defer f.Close()

	approved := make(map[string]bool)
	inTable := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "| Module |") {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			break // the table ended
		}
		if m := skillTableRow.FindStringSubmatch(line); m != nil {
			approved[m[1]] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", goStyleSkillPath, err)
	}
	if len(approved) == 0 {
		t.Fatalf("found no dependency rows in %s; the table heading may have changed", goStyleSkillPath)
	}
	return approved
}

// directRequires reads go.mod's own require block(s) and returns the module
// paths required directly, excluding anything marked "// indirect".
func directRequires(t *testing.T, root string) []string {
	t.Helper()

	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("open go.mod: %v", err)
	}
	defer f.Close()

	var direct []string
	inBlock := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "require ("):
			inBlock = true
		case inBlock && line == ")":
			inBlock = false
		case inBlock:
			if strings.Contains(line, "// indirect") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 1 {
				direct = append(direct, fields[0])
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan go.mod: %v", err)
	}
	return direct
}

// TestGoModDirectDependenciesAreApproved keeps go.mod and the go-style
// skill's Dependencies table from drifting apart, the way the allowlist
// regexp once did (#19, #43). go.mod is not required to use every approved
// module; the table may list one that a future ticket has not added yet. The
// direction that matters is the other one: nothing may reach go.mod without
// first being added to the table, so a dependency can never arrive without
// the review the table's own rationale column exists to record.
func TestGoModDirectDependenciesAreApproved(t *testing.T) {
	root := moduleRoot(t)
	approved := approvedDependencies(t, root)

	for _, mod := range directRequires(t, root) {
		// The table drops the "github.com/" host from a GitHub module path,
		// since every entry there is otherwise obviously one: spf13/cobra,
		// not github.com/spf13/cobra. A module hosted elsewhere, such as
		// modernc.org/sqlite, keeps its own domain in both places.
		short := strings.TrimPrefix(mod, "github.com/")
		if !approved[mod] && !approved[short] {
			t.Errorf("go.mod requires %s directly, but it is not in the Dependencies table in %s.\n"+
				"Ask before adding a new dependency, then add it to that table in the same commit.",
				mod, goStyleSkillPath)
		}
	}
}
