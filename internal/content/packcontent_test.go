package content

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform"
)

// scopedLines returns the solution lines a command_matched or
// command_not_matched check with the given scope would actually see at
// runtime, mirroring internal/verify's parseScope semantics (journal_checks.go)
// without importing that package: internal/content and internal/verify are
// peers by design, neither importing the other.
//
// A check with scope: last only ever sees the single most recent command, and
// last_n:N only the most recent N. Matching every line of the solution
// regardless of scope, as an earlier version of this file did, would report a
// bonus reachable when a scope: last check would in fact never see the line
// that satisfies it, because it is not the last thing the solution ran.
func scopedLines(lines []string, scope string) []string {
	switch {
	case scope == "" || scope == "level":
		return lines
	case scope == "last":
		if len(lines) == 0 {
			return nil
		}
		return lines[len(lines)-1:]
	case strings.HasPrefix(scope, "last_n:"):
		n, err := strconv.Atoi(strings.TrimPrefix(scope, "last_n:"))
		if err != nil || n <= 0 {
			return lines
		}
		if n > len(lines) {
			n = len(lines)
		}
		return lines[len(lines)-n:]
	default:
		// An unparseable scope is a different test's problem (schema
		// validation); do not let it hide a pattern mismatch here.
		return lines
	}
}

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

// scanPathPattern matches an absolute path token: a run of characters that
// looks like a filesystem path, starting at a "/". It is intentionally
// simple, per plan D7: it does not follow a shell variable and it does not
// unwrap a heredoc, so it stops at the first character that cannot appear in
// a bare path token. That is enough to find a literal path written directly
// into a script check's run: body, which is the leak this exists to catch.
var scanPathPattern = regexp.MustCompile(`/[A-Za-z0-9_./-]+`)

// learnerPathsOutsideRoot returns every absolute path in body that is under
// platform.LearnerHomePrefix, or is /home/learner itself, but not under
// root, in the order they appear, reported in their cleaned form.
//
// Every candidate is run through path.Clean before either comparison. An
// earlier version compared the raw regex match instead, so a body containing
// a .. segment, such as root + "/../.shellforge/journal.tsv", passed the raw
// HasPrefix(candidate, root+"/") test and was never flagged even though it
// resolves outside root. Cleaning first closes that gap: containment is
// judged on the same resolved path a shell would actually reach.
//
// A path outside /home/learner/ entirely is deliberately not judged: a level
// may legitimately teach a system path, such as perm-03's /opt/atlas, and
// flagging those would refuse a real curriculum shape rather than the leak
// this helper exists to catch. The scan also does not follow a shell
// variable and does not unwrap a heredoc, so a path built either way is
// invisible to it. TODO(v0.2): whether this graduates into a validator rule
// needs a decision on system paths first.
func learnerPathsOutsideRoot(body, root string) []string {
	clean := path.Clean(root)
	var out []string
	for _, candidate := range scanPathPattern.FindAllString(body, -1) {
		resolved := path.Clean(candidate)
		if resolved != "/home/learner" && !strings.HasPrefix(resolved, platform.LearnerHomePrefix) {
			continue // not under the learner's home at all: out of scope
		}
		if resolved == clean || strings.HasPrefix(resolved, clean+"/") {
			continue // inside this level's own root: fine
		}
		out = append(out, resolved)
	}
	return out
}

// TestLearnerPathsOutsideRoot exercises the helper on its own, table driven,
// before it is trusted to scan every shipped level's script checks.
func TestLearnerPathsOutsideRoot(t *testing.T) {
	const root = "/home/learner/quest"

	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "inside the root is fine",
			body: "find /home/learner/quest/important -type f",
			want: nil,
		},
		{
			name: "the root itself is fine",
			body: "ls /home/learner/quest",
			want: nil,
		},
		{
			name: "a sibling level's world leaks",
			body: "cat /home/learner/other/notes.txt",
			want: []string{"/home/learner/other/notes.txt"},
		},
		{
			name: "the state directory leaks",
			body: "cat /home/learner/.shellforge/journal.tsv",
			want: []string{"/home/learner/.shellforge/journal.tsv"},
		},
		{
			name: "the learner home itself leaks",
			body: "ls /home/learner",
			want: []string{"/home/learner"},
		},
		{
			name: "a system path is not judged",
			body: "wc -l < /dev/null; /bin/bash --version; cat /proc/1/status",
			want: nil,
		},
		{
			name: "a prefix lookalike is not flagged: it is outside /home/learner/ and so out of scope",
			body: "cat /home/learner2/quest/x",
			want: nil,
		},
		{
			name: "several on one line, both reported in order",
			body: "diff /home/learner/other/a.txt /home/learner/another/b.txt",
			want: []string{"/home/learner/other/a.txt", "/home/learner/another/b.txt"},
		},
		{
			name: "a .. traversal out to the state directory is flagged, not defeated by the raw prefix match",
			body: "cat /home/learner/quest/../.shellforge/journal.tsv",
			want: []string{"/home/learner/.shellforge/journal.tsv"},
		},
		{
			name: "a .. traversal out to a sibling level is flagged, not defeated by the raw prefix match",
			body: "cat /home/learner/quest/../other/notes.txt",
			want: []string{"/home/learner/other/notes.txt"},
		},
		{
			name: "a mixed ./ and multiple .. segments still resolves outside the root and is flagged",
			body: "cat /home/learner/quest/./../../learner/other/x",
			want: []string{"/home/learner/other/x"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := learnerPathsOutsideRoot(tc.body, root)
			if !slices.Equal(got, tc.want) {
				t.Errorf("learnerPathsOutsideRoot(%q, %q) = %v, want %v", tc.body, root, got, tc.want)
			}
		})
	}
}

// TestShippedLevelsKeepLearnerOutputInsideTheirRoot enforces the convention
// docs/CURRICULUM.md records: everything a level touches lives under its own
// setup.root.
//
// Teardown removes setup.root and may go no further, so a check pointing at a
// path outside it is describing a file that will survive the level and leak
// into the next one.
//
// A script check's run: body gets the same treatment as path and compare_to,
// through learnerPathsOutsideRoot, which is the narrow scan and stated
// limitation from plan D7: it catches a literal leaked path, not one built
// from a variable or hidden inside a heredoc, and it does not judge a path
// outside /home/learner/ at all, since a level may teach a system path.
func TestShippedLevelsKeepLearnerOutputInsideTheirRoot(t *testing.T) {
	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}

	for i := range pack.Levels {
		lvl := &pack.Levels[i]
		root := path.Clean(lvl.Setup.Root)

		for _, c := range walkChecks(lvl.Checks) {
			// compare_to as well as path. A dir_tree check names two
			// directories and reads both, so covering only path leaves the
			// second one unchecked: files-03 compares config.bak against
			// config, and a compare_to pointing outside the root would have
			// passed this test while describing a directory teardown never
			// removes.
			for _, key := range []string{"path", "compare_to"} {
				target, ok := c.Params[key].(string)
				if !ok || target == "" {
					continue
				}
				clean := path.Clean(target)
				if clean != root && !strings.HasPrefix(clean, root+"/") {
					t.Errorf("%s: %s: check %q names %s in %s, which is outside setup.root %s, so teardown will not clean it up",
						lvl.SourceFile, lvl.ID, c.ID, target, key, root)
				}
			}

			if c.Type != "script" {
				continue
			}
			run, ok := c.Params["run"].(string)
			if !ok || run == "" {
				continue
			}
			for _, leak := range learnerPathsOutsideRoot(run, root) {
				t.Errorf("%s: %s: check %q's run body names %s, which is outside setup.root %s, so teardown will not clean it up",
					lvl.SourceFile, lvl.ID, c.ID, leak, root)
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

// TestScopedLines pins scopedLines against internal/verify's parseScope
// semantics directly, since packcontent_test.go cannot import internal/verify
// to share the implementation and a silent drift between the two would let
// TestCommandMatchedBonusesAreReachableByTheirOwnSolution pass a level whose
// scope: last or scope: last_n check could never actually pass at runtime.
func TestScopedLines(t *testing.T) {
	lines := []string{"cd ~/quest", "grep -r ERROR logs/ | wc -l", "pwd > answer.txt"}

	cases := []struct {
		name  string
		scope string
		want  []string
	}{
		{"empty scope means level", "", lines},
		{"explicit level", "level", lines},
		{"last is only the final line", "last", []string{"pwd > answer.txt"}},
		{"last_n:2 is the final two lines", "last_n:2", []string{"grep -r ERROR logs/ | wc -l", "pwd > answer.txt"}},
		{"last_n larger than the solution is clamped to all of it", "last_n:99", lines},
		{"last_n:0 is invalid and falls back to every line", "last_n:0", lines},
		{"an unparseable scope falls back to every line", "not-a-real-scope", lines},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scopedLines(lines, tc.scope)
			if !slices.Equal(got, tc.want) {
				t.Errorf("scopedLines(lines, %q) = %v, want %v", tc.scope, got, tc.want)
			}
		})
	}

	t.Run("last on an empty solution returns nothing rather than panicking", func(t *testing.T) {
		got := scopedLines(nil, "last")
		if len(got) != 0 {
			t.Errorf("scopedLines(nil, \"last\") = %v, want empty", got)
		}
	})
}

// TestCommandMatchedBonusesAreReachableByTheirOwnSolution closes the class of
// defect nav-01's used-pwd was: an authored bonus objective that the level's
// own reference solution cannot earn.
//
// It needs no container and no journal: only the pack. For every
// command_matched check, at least one line of the level's solution must
// match its pattern, the same way the golden harness's solution runner would
// have to satisfy it once the journal is wired. That is a cheap, permanent
// guard against a check anchored so tightly that solving the level the
// authored way still fails the bonus it is meant to reward.
func TestCommandMatchedBonusesAreReachableByTheirOwnSolution(t *testing.T) {
	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}

	var checked int
	for i := range pack.Levels {
		lvl := &pack.Levels[i]
		lines := strings.Split(strings.TrimRight(lvl.Solution, "\n"), "\n")

		for _, c := range walkChecks(lvl.Checks) {
			if c.Type != "command_matched" {
				continue
			}
			pattern, _ := c.Params["pattern"].(string)
			if pattern == "" {
				continue
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				t.Errorf("%s: %s: check %q has an unparseable pattern %q: %v", lvl.SourceFile, lvl.ID, c.ID, pattern, err)
				continue
			}
			checked++

			scope, _ := c.Params["scope"].(string)
			visible := scopedLines(lines, scope)

			matched := false
			for _, line := range visible {
				if re.MatchString(line) {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("%s: %s: check %q (pattern %q, scope %q) matches no line of the level's own solution the runtime would show it:\n%s\n"+
					"The reference solution cannot earn the bonus it is meant to demonstrate.",
					lvl.SourceFile, lvl.ID, c.ID, pattern, scope, lvl.Solution)
			}
		}
	}

	if checked == 0 {
		t.Error("no command_matched check was found in the shipped pack, so this test proved nothing; it should be covering nav-01's used-pwd")
	}
}

// TestCommandNotMatchedGuardsDoNotCondemnTheirOwnSolution is the inverse
// check: an anti-pattern guard that matches the level's own reference
// solution would fail the learner for doing it the intended way.
func TestCommandNotMatchedGuardsDoNotCondemnTheirOwnSolution(t *testing.T) {
	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}

	var checked int
	for i := range pack.Levels {
		lvl := &pack.Levels[i]
		lines := strings.Split(strings.TrimRight(lvl.Solution, "\n"), "\n")

		for _, c := range walkChecks(lvl.Checks) {
			if c.Type != "command_not_matched" {
				continue
			}
			pattern, _ := c.Params["pattern"].(string)
			if pattern == "" {
				continue
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				t.Errorf("%s: %s: check %q has an unparseable pattern %q: %v", lvl.SourceFile, lvl.ID, c.ID, pattern, err)
				continue
			}
			checked++

			scope, _ := c.Params["scope"].(string)
			visible := scopedLines(lines, scope)

			for _, line := range visible {
				if re.MatchString(line) {
					t.Errorf("%s: %s: check %q (pattern %q, scope %q) matches a line of the level's own solution the runtime would show it: %q\n"+
						"The anti-pattern guard would condemn the reference answer.",
						lvl.SourceFile, lvl.ID, c.ID, pattern, scope, line)
				}
			}
		}
	}

	if checked == 0 {
		t.Error("no command_not_matched check was found in the shipped pack, so this test proved nothing; it should be covering pipe-05's nocheat")
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
