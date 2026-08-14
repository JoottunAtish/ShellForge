package content

import (
	"strings"
	"testing"
)

// What a setup script may assume, held against every level in the shipped pack.
//
// A setup script runs as the LEARNER, in the level root, after setup.files have
// been materialized and chowned. It is not root, and it cannot become root: the
// sandbox starts with --cap-drop ALL and adds back only CHOWN and FOWNER, so
// root inside it does not hold CAP_DAC_OVERRIDE and cannot even write into the
// learner-owned level root. docs/LEVEL-FORMAT.md's "Setup and teardown
// execution" section is the contract, and internal/content/setup's runScript
// carries the mechanism.
//
// These tests exist because the failure mode is SILENT. No `set -e` is injected,
// by a deliberate documented decision, so a script's exit status is its last
// command's. files-03 and files-04 both shipped with `mkdir -p <dir>` followed
// by `chown -R learner:learner .`: the mkdir failed with EACCES as root, the
// chown succeeded because CAP_CHOWN is kept, bash returned zero, and setup
// reported success while the directory the level's own solution needed was never
// created. Nothing caught it until the golden harness ran the levels in a real
// container.
//
// The golden harness catches it now, but it needs Docker and so it does not run
// on the fast test job or on the Windows leg. These do, and they fail on the
// commit that introduces the pattern rather than on the one that finally runs a
// container.

// TestNoSetupScriptChownsTheLevelRoot asserts no level's setup or teardown
// script tries to chown.
//
// A chown in a level script is always one of two things, and neither should
// ship. It is either redundant, because the runner chowns everything
// setup.files materialized before the script starts, or it is the trailing
// command that masks an earlier failure, which is exactly how files-03 and
// files-04 hid. The learner also cannot chown to another user at all, so a
// script that means it will fail.
func TestNoSetupScriptChownsTheLevelRoot(t *testing.T) {
	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}
	if len(pack.Levels) == 0 {
		t.Fatal("the embedded pack has no levels; this test is not looking at anything")
	}

	for _, lvl := range pack.Levels {
		for _, s := range []struct{ what, script string }{
			{"setup.script", lvl.Setup.Script},
			{"teardown.script", lvl.Teardown.Script},
		} {
			if !strings.Contains(s.script, "chown") {
				continue
			}
			t.Errorf("%s: %s runs chown.\n"+
				"The runner already chowns everything setup.files materialized to the learner "+
				"before the script runs, so this is redundant at best. At worst it is the last "+
				"command in the script, which makes the script exit zero and hides any command "+
				"before it that failed: that is how files-03 and files-04 shipped without the "+
				"directory their own solutions needed. Delete the chown.", lvl.ID, s.what)
		}
	}
}

// TestNoSetupScriptNeedsRoot asserts no level script uses a command that only
// root could run.
//
// The list is short on purpose, and it is about privilege rather than danger:
// each of these needs a capability the sandbox does not give the learner, so a
// level using one is broken rather than risky. sudo is on it because the image
// does grant the learner passwordless sudo for the LEARNER's own exercises, and
// a setup script quietly relying on it would make the level's starting state
// depend on a privilege escalation path that levels about permissions may later
// want to take away.
func TestNoSetupScriptNeedsRoot(t *testing.T) {
	needsRoot := map[string]string{
		"sudo":     "the script would depend on the learner's sudo grant, which a later level may remove",
		"useradd":  "creating users needs privileges the sandbox withholds",
		"groupadd": "creating groups needs privileges the sandbox withholds",
		"mount":    "mounting needs CAP_SYS_ADMIN, which the sandbox drops",
		"chroot":   "chroot needs CAP_SYS_CHROOT, which the sandbox drops",
	}

	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}

	for _, lvl := range pack.Levels {
		for _, s := range []struct{ what, script string }{
			{"setup.script", lvl.Setup.Script},
			{"teardown.script", lvl.Teardown.Script},
		} {
			for _, field := range strings.Fields(s.script) {
				reason, bad := needsRoot[field]
				if !bad {
					continue
				}
				t.Errorf("%s: %s runs %q, which will not work: %s.\n"+
					"Level scripts run as the learner. See the setup and teardown execution "+
					"section of docs/LEVEL-FORMAT.md.", lvl.ID, s.what, field, reason)
			}
		}
	}
}

// TestMultiCommandSetupScriptsSetErrexit asserts that a setup script with more
// than one command opts into `set -e`.
//
// No `set -e` is injected, which docs/LEVEL-FORMAT.md decided deliberately: it
// would change the meaning of every example in that document. The consequence is
// that a multi-command script reports only its last command's status, so a
// failure anywhere earlier is discarded and the level ships half built. That is
// not a hypothetical trade-off, it is what happened to files-03 and files-04.
//
// So the injection stays the author's call and this test makes the call
// explicit: one command needs nothing, more than one has to say `set -e`. A
// single-command script cannot hide anything, which is why it is exempt rather
// than grandfathered.
func TestMultiCommandSetupScriptsSetErrexit(t *testing.T) {
	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}

	for _, lvl := range pack.Levels {
		for _, s := range []struct{ what, script string }{
			{"setup.script", lvl.Setup.Script},
			{"teardown.script", lvl.Teardown.Script},
		} {
			commands := 0
			errexit := false
			for _, line := range strings.Split(s.script, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				if line == "set -e" || strings.HasPrefix(line, "set -e") {
					errexit = true
					continue
				}
				commands++
			}

			if commands > 1 && !errexit {
				t.Errorf("%s: %s has %d commands and no `set -e`.\n"+
					"Without it the script's exit status is only its LAST command's, so a failure "+
					"in any earlier one is silently discarded and the level ships with a world it "+
					"never finished building. Add `set -e` as the first line, or reduce the script "+
					"to one command.", lvl.ID, s.what, commands)
			}
		}
	}
}

// TestPreservesObjectivesAreNotAlsoOptional asserts the two flags are never set
// together.
//
// They pull in opposite directions and a level setting both is confused rather
// than expressive. `optional` means a learner can skip it and still pass;
// `preserves` means the objective is already satisfied and the learner must not
// break it. Both at once describes a bonus that is awarded for doing nothing,
// which is not a thing worth awarding, and it would also make the golden
// contract skip the objective entirely: `optional` is checked first, so the
// preservation assertion would never run and the level would lose the only gate
// it had.
func TestPreservesObjectivesAreNotAlsoOptional(t *testing.T) {
	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}
	if len(pack.Levels) == 0 {
		t.Fatal("the embedded pack has no levels; this test is not looking at anything")
	}

	for _, lvl := range pack.Levels {
		for _, obj := range lvl.Objectives {
			if obj.Preserves && obj.Optional {
				t.Errorf("%s: objective %q sets both `optional` and `preserves`.\n"+
					"They contradict each other: optional means the learner may skip it, preserves "+
					"means it is already true and must stay true. Setting both also makes the golden "+
					"contract skip the objective, since optional is tested first, so the level would "+
					"be left with no assertion about it at all. Pick one.", lvl.ID, obj.ID)
			}
		}
	}
}

// TestEveryPreservingObjectiveIsADestructiveLevel is a smell test rather than a
// rule, and it is deliberately narrow.
//
// `preserves` exempts an objective from the golden contract's strongest
// assertion, which makes it the one field in the schema an author could reach for
// to make a failing golden test go away. The legitimate use is a level that asks
// the learner to destroy things carefully, so requiring the level to say
// `destructive` in its tags costs an honest author one word and makes a
// dishonest use visible in review.
func TestEveryPreservingObjectiveIsADestructiveLevel(t *testing.T) {
	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}

	for _, lvl := range pack.Levels {
		preserving := false
		for _, obj := range lvl.Objectives {
			if obj.Preserves {
				preserving = true
				break
			}
		}
		if !preserving {
			continue
		}

		destructive := false
		for _, tag := range lvl.Tags {
			if tag == "destructive" {
				destructive = true
				break
			}
		}
		if !destructive {
			t.Errorf("%s has a `preserves: true` objective but is not tagged `destructive`.\n"+
				"preserves exempts an objective from the golden contract's per-objective "+
				"pre-solution assertion, which is the assertion that catches a check testing "+
				"nothing. The honest use is a level about deleting things carefully. If that is "+
				"what this level is, add the destructive tag; if it is not, the objective almost "+
				"certainly wants a real check rather than an exemption.", lvl.ID)
		}
	}
}
