package content

import (
	"strings"
	"testing"
)

// TestCycleErrorNamesOnlyTheLevelsInTheCycle guards #93 item 5: the cycle
// error must name only the levels that are actually on a cycle, not every
// level left with residual in-degree by a bystander prerequisite.
//
// nav-01 and nav-02 require each other, a genuine two level cycle. nav-03
// requires nav-02 but is not itself required by anything on the cycle, so it
// is stuck (topoSort can never place it) without being part of the cycle.
// nav-04 has no prerequisite at all and proves the narrowing is not simply
// "everything with a prerequisite".
func TestCycleErrorNamesOnlyTheLevelsInTheCycle(t *testing.T) {
	first := defaultLevel()
	first.Extra = "prerequisites: [nav-02]\n"
	second := defaultLevel()
	second.ID = "nav-02"
	second.Extra = "prerequisites: [nav-01]\n"
	third := defaultLevel()
	third.ID = "nav-03"
	third.Extra = "prerequisites: [nav-02]\n"
	fourth := defaultLevel()
	fourth.ID = "nav-04"

	acts := "  - id: act1\n    title: \"Act One\"\n    levels: [nav-01, nav-02, nav-03, nav-04]\n"
	pack := loadFixture(t, fixturePack(acts, first, second, third, fourth))

	_, err := pack.Order()
	if err == nil {
		t.Fatal("a prerequisite cycle was accepted")
	}

	for _, id := range []string{"nav-01", "nav-02"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("the cycle error should name %q, got %q", id, err)
		}
	}
	for _, id := range []string{"nav-03", "nav-04"} {
		if strings.Contains(err.Error(), id) {
			t.Errorf("the cycle error should not name %q, which is not on the cycle: %q", id, err)
		}
	}
}
