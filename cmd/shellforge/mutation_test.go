package main

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/JoottunAtish/ShellForge/internal/content"
	"github.com/JoottunAtish/ShellForge/internal/game"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/verify"
)

// The mutation test: the accepts-wrong-answer check for the levels whose
// checks are strict enough that a plausible mistake could slip past them
// undetected.
//
// This is what the testing skill calls a mutation test: feed a known near
// miss into a level that already passed, and assert the level rejects it. A
// check that stays green after the world is deliberately wrong is an
// over-permissive check, and an over-permissive check is the quiet failure
// mode of a teaching tool: it tells a learner they are right when they are
// not.

// nearMiss is one wrong answer, run as bash inside the sandbox after the
// level's own solution has been applied clean.
type nearMiss struct {
	// name is the subtest name.
	name string

	// script is the wrong answer, run as bash -lc, as the learner.
	script string

	// explain is the mistake a real learner would have made to end up here.
	explain string

	// commands overrides the solution-derived journal for this case. Empty
	// means use the level's own solution, split by solutionCommands.
	commands []string

	// wantNote is a check id. When set, the case asserts the level PASSED
	// and that check's on_fail landed in Notes, rather than asserting
	// rejection. Empty means the ordinary case: assert rejection.
	wantNote string
}

// mutationCase is one level's table of near misses.
type mutationCase struct {
	levelID string
	misses  []nearMiss
}

// mutationTable is the whole table. Package level so
// TestMutationTableCoversTheRequiredLevels can read it without a container.
var mutationTable = []mutationCase{
	{
		levelID: "pipe-05",
		misses: []nearMiss{
			{
				name:    "the count off by one",
				script:  `printf '146\n' > ~/quest/report.txt`,
				explain: "a learner who missed a file, or counted with the wrong flag",
			},
			{
				name:    "the count off by one the other way",
				script:  `printf '148\n' > ~/quest/report.txt`,
				explain: "a learner who counted a header line",
			},
			{
				name:    "an empty report",
				script:  `: > ~/quest/report.txt`,
				explain: "a redirect that ran before the pipeline",
			},
			{
				name:    "the codes lowercase",
				script:  `printf 'e401\ne500\ne503\n' > ~/quest/codes.txt`,
				explain: "grep -o without -E, or a pattern that lowercased",
			},
			{
				name:    "the codes unsorted",
				script:  `printf 'E503\nE401\nE500\n' > ~/quest/codes.txt`,
				explain: "uniq without sort, which only collapses adjacent duplicates",
			},
			{
				name:    "the codes with duplicates",
				script:  `printf 'E401\nE401\nE500\nE503\n' > ~/quest/codes.txt`,
				explain: "sort without -u",
			},
			{
				name:    "a fourth code that is not in the logs",
				script:  `printf 'E401\nE404\nE500\nE503\n' > ~/quest/codes.txt`,
				explain: "a pattern that matched something else in the line",
			},
		},
	},
	{
		levelID: "files-04",
		misses: []nearMiss{
			{
				name:    "a tmp truncated instead of deleted",
				script:  `: > ~/quest/render-8817.tmp`,
				explain: "rm replaced with a truncating redirect, which leaves the name behind",
			},
			{
				name:    "one tmp left behind",
				script:  `printf 'half-written export\n' > ~/quest/export-queue.tmp`,
				explain: "a glob that missed one of the three .tmp names",
			},
			{
				name:    "the empty directory recreated",
				script:  `mkdir -p ~/quest/scratch`,
				explain: "rmdir ran, and then something recreated the directory",
			},
			{
				name:    "cache emptied but the directory left standing",
				script:  `mkdir -p ~/quest/cache`,
				explain: "the files were deleted one by one and the directory itself was never removed",
			},
			{
				name:    "an important file rewritten with different bytes",
				script:  `printf 'Meridian Logistics, signed customer contracts.\n' > ~/quest/important/customer-contracts.txt`,
				explain: "a delete that went wider than intended and a restore that did not reproduce the original bytes",
			},
			{
				name:    "a fourth file added to important",
				script:  `printf 'stray note\n' > ~/quest/important/scratch-note.txt`,
				explain: "a stray file left in important/ where only the original three belong",
			},
		},
	},
	{
		levelID: "find-04",
		misses: []nearMiss{
			{
				name:    "a nested temporary left behind",
				script:  `printf 'scratch\n' > ~/quest/data/inbound/2026-01/batch-01/raw/part-a.tmp`,
				explain: "a find that only looked at the top of data/ rather than five levels deep",
			},
			{
				name:    "chmod -R +x took the README with it",
				script:  `chmod -R +x ~/quest/scripts`,
				explain: "a recursive chmod pointed at scripts/ instead of a find -exec that named only the .sh files",
			},
			{
				name:    "a delete with no -name test took a real record",
				script:  `rm -f ~/quest/data/inbound/2026-01/batch-01/rows.csv`,
				explain: "a find -delete with no -name test, which takes everything under the path",
			},
			{
				name:    "the count of only one half",
				script:  `printf '14\n' > ~/quest/touched.txt`,
				explain: "counting the deletions and forgetting the scripts made executable, or the reverse",
			},
			{
				name:    "the count with a word after it",
				script:  `printf '22 files\n' > ~/quest/touched.txt`,
				explain: "a script that computed the right number and then printed it with a label attached",
			},
			{
				name:     "the count typed in rather than counted",
				script:   `cd ~/quest && echo 22 > touched.txt`,
				explain:  "the right number, arrived at by memory rather than by counting",
				commands: []string{"cd ~/quest", "echo 22 > touched.txt"},
				wantNote: "no-hardcoded-count",
			},
		},
	},
	{
		levelID: "perm-01",
		misses: []nearMiss{
			{
				name:    "deploy.sh readable by everyone",
				script:  `chmod 755 ~/quest/deploy.sh`,
				explain: "a chmod that left the group and other bits alone instead of clearing them",
			},
			{
				name:    "notes.txt still writable by its owner",
				script:  `chmod 644 ~/quest/notes.txt`,
				explain: "a chmod that granted read to everyone but did not take write away from the owner",
			},
			{
				name:    "secrets.env readable by the group",
				script:  `chmod 640 ~/quest/secrets.env`,
				explain: "a chmod that left the group with read access instead of locking the file to the owner alone",
			},
			{
				name:    "the right nine bits with setuid on top",
				script:  `chmod 4700 ~/quest/deploy.sh`,
				explain: "a fourth octal digit added on top of the right nine bits, changing more than was asked",
			},
			{
				name: "the right modes applied to a copy",
				script: `mkdir -p ~/quest/audit && cp ~/quest/deploy.sh ~/quest/audit/deploy.sh && ` +
					`chmod 700 ~/quest/audit/deploy.sh && chmod 644 ~/quest/deploy.sh`,
				explain: "the mode set on a copy of the file rather than on the file the level names",
			},
			{
				name:     "an all-octal solve",
				script:   `chmod 444 ~/quest/notes.txt`,
				explain:  "every mode set with a number, when the bonus objective asks for symbolic notation at least once",
				commands: []string{"cd ~/quest", "chmod 700 deploy.sh", "chmod 444 notes.txt", "chmod 600 secrets.env"},
				wantNote: "used-symbolic",
			},
		},
	},
	{
		levelID: "script-01",
		misses: []nearMiss{
			{
				name: "a hardcoded count",
				script: `cat > ~/toolbox/bin/count-logs.sh <<'SCRIPT'
#!/bin/bash
echo 5
SCRIPT
chmod +x ~/toolbox/bin/count-logs.sh`,
				explain: "a script that got the right answer for logs/ once and typed it in rather than counting",
			},
			{
				name: "no -name test, so it counts the two non-logs",
				script: `cat > ~/toolbox/bin/count-logs.sh <<'SCRIPT'
#!/bin/bash
if [ $# -lt 1 ]; then
    echo "usage: count-logs.sh <directory>" >&2
    exit 1
fi
if [ ! -d "$1" ]; then
    echo "count-logs.sh: no such directory: $1" >&2
    exit 1
fi
find "$1" -maxdepth 1 -type f | wc -l
SCRIPT
chmod +x ~/toolbox/bin/count-logs.sh`,
				explain: "a find with no -name test, which counts README.md and rotate.conf along with the .log files",
			},
			{
				name: "no -maxdepth 1, so it counts the archive",
				script: `cat > ~/toolbox/bin/count-logs.sh <<'SCRIPT'
#!/bin/bash
if [ $# -lt 1 ]; then
    echo "usage: count-logs.sh <directory>" >&2
    exit 1
fi
if [ ! -d "$1" ]; then
    echo "count-logs.sh: no such directory: $1" >&2
    exit 1
fi
find "$1" -type f -name '*.log' | wc -l
SCRIPT
chmod +x ~/toolbox/bin/count-logs.sh`,
				explain: "a find with no -maxdepth 1, which also counts the nested archive log",
			},
			{
				name: "the complaint on stdout instead of stderr",
				script: `cat > ~/toolbox/bin/count-logs.sh <<'SCRIPT'
#!/bin/bash
if [ $# -lt 1 ]; then
    echo "usage: count-logs.sh <directory>"
    exit 1
fi
if [ ! -d "$1" ]; then
    echo "count-logs.sh: no such directory: $1"
    exit 1
fi
find "$1" -maxdepth 1 -type f -name '*.log' | wc -l
SCRIPT
chmod +x ~/toolbox/bin/count-logs.sh`,
				explain: "an echo that forgot the >&2 redirect, so the error lands on stdout instead of stderr",
			},
			{
				name: "exit 2 instead of exit 1",
				script: `cat > ~/toolbox/bin/count-logs.sh <<'SCRIPT'
#!/bin/bash
if [ $# -lt 1 ]; then
    echo "usage: count-logs.sh <directory>" >&2
    exit 2
fi
if [ ! -d "$1" ]; then
    echo "count-logs.sh: no such directory: $1" >&2
    exit 2
fi
find "$1" -maxdepth 1 -type f -name '*.log' | wc -l
SCRIPT
chmod +x ~/toolbox/bin/count-logs.sh`,
				explain: "a status code picked without reading that the ticket asked for exactly 1",
			},
			{
				name: "no shebang",
				script: `cat > ~/toolbox/bin/count-logs.sh <<'SCRIPT'
# Counts the .log files directly inside the directory given as $1.
if [ $# -lt 1 ]; then
    echo "usage: count-logs.sh <directory>" >&2
    exit 1
fi
if [ ! -d "$1" ]; then
    echo "count-logs.sh: no such directory: $1" >&2
    exit 1
fi
find "$1" -maxdepth 1 -type f -name '*.log' | wc -l
SCRIPT
chmod +x ~/toolbox/bin/count-logs.sh`,
				explain: "a comment left above the shebang, so it is no longer the first line of the file",
			},
			{
				name:    "the execute bit missing",
				script:  `chmod -x ~/toolbox/bin/count-logs.sh`,
				explain: "the file written correctly and chmod +x forgotten",
			},
			{
				name:    "the script in the wrong directory",
				script:  `mv ~/toolbox/bin/count-logs.sh ~/toolbox/count-logs.sh`,
				explain: "the file written correctly but never moved into bin/, where every check expects to find it",
			},
		},
	},
}

// requiredMutationLevels is the set the testing skill names. Kept separate
// from mutationTable so a level dropped from the table is a failure rather
// than a silently smaller table.
var requiredMutationLevels = []string{"pipe-05", "files-04", "find-04", "perm-01", "script-01"}

// TestMutationTableCoversTheRequiredLevels is hermetic: it needs no
// container, because it is a question about the table and the pack rather
// than about the sandbox. It is the one part 1 test that runs locally.
func TestMutationTableCoversTheRequiredLevels(t *testing.T) {
	pack, err := content.Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}

	casesByLevel := make(map[string][]nearMiss, len(mutationTable))
	for _, c := range mutationTable {
		casesByLevel[c.levelID] = c.misses
	}

	for _, id := range requiredMutationLevels {
		misses, ok := casesByLevel[id]
		if !ok {
			t.Errorf("mutationTable has no entry for required level %q", id)
			continue
		}
		if len(misses) == 0 {
			t.Errorf("mutationTable's entry for %q has no cases", id)
		}
	}

	for _, c := range mutationTable {
		if _, ok := pack.Level(c.levelID); !ok {
			t.Errorf("mutationTable names level %q, which is not in the embedded pack", c.levelID)
		}
	}
}

// TestLevelsRejectNearMisses is the accepts-wrong-answer check, over the five
// levels the testing skill names.
//
// Subtests are deliberately NOT parallel, for the same reason
// TestEveryLevelGoldenPath's are not: one container, and two levels setting
// up in it at once would collide over /home/learner and the state directory.
func TestLevelsRejectNearMisses(t *testing.T) {
	requireGoldenSandbox(t)

	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()

	pack, err := content.Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}
	packFS, err := goldenPackFS()
	if err != nil {
		t.Fatalf("root the pack filesystem: %v", err)
	}

	sess := goldenSession(t, ctx)

	for _, c := range mutationTable {
		c := c
		t.Run(c.levelID, func(t *testing.T) {
			level, ok := pack.Level(c.levelID)
			if !ok {
				t.Fatalf("%s is not in the embedded pack", c.levelID)
			}

			sj := newSolutionJournal()
			session, err := game.NewSession(game.Config{
				Level:    level,
				Sess:     sess,
				PackFS:   packFS,
				Verifier: verify.NewEngine(),
				Journal:  sj,
			})
			if err != nil {
				t.Fatalf("build the level: %v", err)
			}
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
				defer cancel()
				_ = session.Teardown(cleanupCtx)
			})

			for _, miss := range c.misses {
				miss := miss
				t.Run(miss.name, func(t *testing.T) {
					// Setup runs per case, not once per level. setup.Runner
					// is teardown-first and safe to call twice in a row, and
					// several cases here leave residue the level's own
					// solution cannot undo on its own: files-04's solution
					// only deletes, so it cannot restore a tampered
					// important/customer-contracts.txt or remove a fourth
					// file added to important/. Without a per-case Setup, one
					// case poisons every later case in the same level.
					if err := session.Setup(ctx); err != nil {
						t.Fatalf("setup: %v", err)
					}
					if err := applySolution(ctx, sess, level); err != nil {
						t.Fatalf("apply the solution: %v", err)
					}

					commands := miss.commands
					if len(commands) == 0 {
						commands = solutionCommands(level.Solution)
					}
					sj.Record(commands)

					if _, err := sess.Exec(ctx, []string{"bash", "-lc", miss.script}, runtime.ExecOpts{
						User: sandboxUser, WorkDir: level.Setup.Root, Timeout: 30 * time.Second,
					}); err != nil {
						t.Fatalf("write the near miss: %v", err)
					}

					res, err := session.Check(ctx)
					if err != nil {
						t.Fatalf("check: %v", err)
					}

					if miss.wantNote == "" {
						if res.Passed {
							t.Errorf("%s accepted a wrong answer (%s: %s).\n"+
								"      The level accepts something it should not. Tighten the check, and confirm it "+
								"still accepts every legitimate way to reach the right answer.",
								c.levelID, miss.name, miss.explain)
						}
						return
					}

					// A wantNote case is not asserting rejection: the check
					// named by wantNote is severity: warn or optional, so it
					// cannot fail the level. Asserting rejection here would
					// be asserting a bug into existence, since the engine
					// never lets either kind of check block Passed.
					if !res.Passed {
						t.Errorf("%s (%s: %s) should have passed, with only %q's non-blocking note firing, but the level reported not passed. Notes: %v",
							c.levelID, miss.name, miss.explain, miss.wantNote, res.Notes)
						return
					}

					onFail, ok := onFailFor(level, miss.wantNote)
					if !ok {
						t.Fatalf("level %q has no check %q to read on_fail from", c.levelID, miss.wantNote)
					}
					want := noteSnippet(onFail)
					found := false
					for _, note := range res.Notes {
						if strings.Contains(note, want) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("%s (%s: %s) passed, but Notes does not contain %q's on_fail (looking for %q). Notes: %v",
							c.levelID, miss.name, miss.explain, miss.wantNote, want, res.Notes)
					}
				})
			}
		})
	}
}

// onFailFor returns the on_fail text the level author wrote for the
// top-level check with this id. It is what the engine puts in
// LevelResult.Notes for a non-blocking failure, so it is what a wantNote case
// matches against.
func onFailFor(level *content.Level, checkID string) (string, bool) {
	for i := range level.Checks {
		if level.Checks[i].ID == checkID {
			return level.Checks[i].OnFail, true
		}
	}
	return "", false
}

// noteSnippet returns a distinctive leading slice of an on_fail, collapsed to
// single spaces, for a substring match against Notes. Matching a slice rather
// than the whole string keeps a copy edit to on_fail from breaking the test
// while still proving the right note fired.
func noteSnippet(onFail string) string {
	collapsed := strings.Join(strings.Fields(onFail), " ")

	const maxBytes = 48
	if len(collapsed) <= maxBytes {
		return collapsed
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(collapsed[end]) {
		end--
	}
	return collapsed[:end]
}
