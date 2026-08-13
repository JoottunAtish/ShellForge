package content

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
	"testing"
)

// TestLevelAssetHashesMatchTheirContent keeps a level's declared sha256 from
// drifting away from the asset it describes.
//
// files-04 asserts that important/ survives byte for byte by hashing each of
// its three files, and those files are authored as inline content in the same
// YAML. Nothing else connects the two: editing a word of Kofi's handover note
// without recomputing the hash would leave a level that can never be passed,
// and the only way to discover it would be to play it. This closes that gap
// at build time.
//
// It runs over every level, so a future level that hashes an asset is covered
// the day it is written, with no test to remember to add.
func TestLevelAssetHashesMatchTheirContent(t *testing.T) {
	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}

	var checked int
	for i := range pack.Levels {
		lvl := &pack.Levels[i]

		// The materialized path of every inline asset, so a check's
		// absolute path can be matched back to the content it hashes.
		inline := map[string]string{}
		for _, f := range lvl.Setup.Files {
			if f.ContentSet {
				inline[path.Join(lvl.Setup.Root, f.Path)] = f.Content
			}
		}

		for _, c := range walkChecks(lvl.Checks) {
			if c.Type != "file_content" {
				continue
			}
			match, _ := c.Params["match"].(string)
			if match != "sha256" {
				continue
			}

			target, _ := c.Params["path"].(string)
			body, ok := inline[target]
			if !ok {
				// The asset comes from a source: file or a generator.
				// Neither can be hashed here without reaching into
				// internal/content/setup, which is a layer this package
				// does not import.
				continue
			}

			want, _ := c.Params["value"].(string)
			sum := sha256.Sum256([]byte(body))
			if got := hex.EncodeToString(sum[:]); got != want {
				t.Errorf("%s: %s: check %q hashes %s\n  declared: %s\n  actual:   %s\n"+
					"The inline content and the declared hash have drifted. Recompute the hash, and bump the level's version so recorded best scores are invalidated.",
					lvl.SourceFile, lvl.ID, c.ID, target, want, got)
			}
			checked++
		}
	}

	if checked == 0 {
		t.Error("no sha256 content check was found in the shipped pack, so this test proved nothing; it should be covering files-04")
	}
}

// TestShippedLevelsKeepLearnerOutputInsideTheirRoot enforces the convention
// docs/CURRICULUM.md records: everything a level touches lives under its own
// setup.root.
//
// Teardown removes setup.root and may go no further, so a check pointing at a
// path outside it is describing a file that will survive the level and leak
// into the next one.
func TestShippedLevelsKeepLearnerOutputInsideTheirRoot(t *testing.T) {
	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}

	for i := range pack.Levels {
		lvl := &pack.Levels[i]
		root := path.Clean(lvl.Setup.Root)

		for _, c := range walkChecks(lvl.Checks) {
			target, ok := c.Params["path"].(string)
			if !ok || target == "" {
				continue
			}
			clean := path.Clean(target)
			if clean != root && !strings.HasPrefix(clean, root+"/") {
				t.Errorf("%s: %s: check %q points at %s, which is outside setup.root %s, so teardown will not clean it up",
					lvl.SourceFile, lvl.ID, c.ID, target, root)
			}
		}
	}
}

// TestShippedLevelsHaveDistinctRoots keeps one level's teardown from deleting
// another level's world.
func TestShippedLevelsHaveDistinctRoots(t *testing.T) {
	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}

	// A shared root is legal and common: several levels use ~/quest, and
	// setup tears down before it builds, so playing one after another is
	// fine. What is not fine is one root containing another, where tearing
	// down the outer one silently destroys the inner one.
	roots := map[string]string{}
	for i := range pack.Levels {
		lvl := &pack.Levels[i]
		root := path.Clean(lvl.Setup.Root)

		for other, otherID := range roots {
			if other == root {
				continue
			}
			if strings.HasPrefix(root, other+"/") || strings.HasPrefix(other, root+"/") {
				t.Errorf("%s: level %q has root %s, which overlaps level %q's root %s: tearing one down would destroy the other",
					lvl.SourceFile, lvl.ID, root, otherID, other)
			}
		}
		roots[root] = lvl.ID
	}
}

// walkChecks flattens a level's check tree, so a test can reason about every
// check regardless of how deeply it is composed.
//
// It recurses through CheckSpec.Branches rather than reaching into AnyOf,
// AllOf and Not by hand. An earlier version did the latter and missed a
// not: wrapping another not:, which is the kind of gap that makes a test
// pass by not looking rather than by finding nothing.
func walkChecks(checks []CheckSpec) []*CheckSpec {
	var out []*CheckSpec
	var walk func(c *CheckSpec)
	walk = func(c *CheckSpec) {
		out = append(out, c)
		for _, b := range c.Branches() {
			walk(b)
		}
	}
	for i := range checks {
		walk(&checks[i])
	}
	return out
}
