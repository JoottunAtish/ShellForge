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
