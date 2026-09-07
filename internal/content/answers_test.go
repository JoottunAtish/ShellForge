package content

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/packs"
)

// Day 5 added three more levels whose answers are counted out of committed
// asset bytes rather than out of inline content: pipe-03, pipe-04 and find-01.
// pipe05_assets_test.go already does this job for pipe-05 and explains why at
// length; the short version is that nothing else connects an asset to the
// answer a check asserts, so editing a log file leaves a level nobody can pass
// and the only way to find out is to play it against a real container.
//
// These recompute each answer the way the level's own solution computes it,
// from the committed bytes, with no Docker, and fail when the asset and the
// check disagree. They run on the Windows matrix leg too, which the golden test
// cannot.
//
// Deliberately not covered here: find-02's leaked key, whose asset is assembled
// by setup.script at play time rather than committed, and find-03's mtimes,
// which are set relative to when the level is played. Neither can be recomputed
// from bytes in the repository, and both belong to the golden test.

// asset reads one file out of the embedded pack.
func asset(t *testing.T, name string) string {
	t.Helper()

	data, err := packs.FS.ReadFile(packs.CoreLinuxBasics + "/" + name)
	if err != nil {
		t.Fatalf("read %s from the embedded pack: %v", name, err)
	}
	if len(data) == 0 {
		t.Fatalf("%s is empty", name)
	}
	return string(data)
}

// assetLines splits an asset into lines, dropping the trailing empty one that a
// file ending in a newline produces.
func assetLines(t *testing.T, name string) []string {
	t.Helper()

	body := strings.TrimSuffix(asset(t, name), "\n")
	if body == "" {
		return nil
	}
	return strings.Split(body, "\n")
}

// levelByID returns a level from the embedded pack.
func levelByID(t *testing.T, id string) *Level {
	t.Helper()

	pack, err := Embedded()
	if err != nil {
		t.Fatalf("load the embedded pack: %v", err)
	}
	lvl, ok := pack.Level(id)
	if !ok {
		t.Fatalf("%s is not in the embedded pack", id)
	}
	return lvl
}

// declaredValue returns the `value` param of the named check on the named
// level, so a test can compare the authored answer against a recomputed one.
func declaredValue(t *testing.T, levelID, checkID string) string {
	t.Helper()

	lvl := levelByID(t, levelID)
	for _, c := range walkChecks(lvl.Checks) {
		if c.ID != checkID {
			continue
		}
		raw, ok := c.Params["value"]
		if !ok {
			t.Fatalf("%s: check %q has no value param", levelID, checkID)
		}
		s, ok := raw.(string)
		if !ok {
			t.Fatalf("%s: check %q value param is %T, want a string", levelID, checkID, raw)
		}
		return s
	}
	t.Fatalf("%s has no check with id %q", levelID, checkID)
	return ""
}

// firstField returns everything before the first space, which is the client
// address in a common log format line.
func firstField(line string) string {
	if i := strings.IndexByte(line, ' '); i >= 0 {
		return line[:i]
	}
	return line
}

// TestPipe03UniqueAddressCountMatchesTheCommittedLog counts distinct addresses
// the way the level's solution does, `cut -d' ' -f1 | sort -u | wc -l`.
func TestPipe03UniqueAddressCountMatchesTheCommittedLog(t *testing.T) {
	seen := map[string]bool{}
	for _, line := range assetLines(t, "assets/access.log") {
		seen[firstField(line)] = true
	}

	declared := strings.TrimSpace(declaredValue(t, "pipe-03", "unique-count"))
	want, err := strconv.Atoi(declared)
	if err != nil {
		t.Fatalf("pipe-03's unique-count check declares %q, which is not a number: %v", declared, err)
	}
	if len(seen) != want {
		t.Errorf("access.log holds %d distinct addresses and pipe-03 asserts %d.\n"+
			"The asset and the check have drifted. Fix the asset or the answer, never by loosening the check, "+
			"and bump the level's version so recorded best scores are invalidated.", len(seen), want)
	}
}

// TestPipe03TopThreeMatchesTheCommittedLog recomputes the busiest three the way
// `sort | uniq -c | sort -rn | head -3` does, and asserts the level's script
// check is looking for those three lines.
//
// The check normalizes whitespace before comparing, so this compares against
// the normalized form: a count, one space, an address.
func TestPipe03TopThreeMatchesTheCommittedLog(t *testing.T) {
	counts := map[string]int{}
	for _, line := range assetLines(t, "assets/access.log") {
		counts[firstField(line)]++
	}

	type entry struct {
		addr string
		n    int
	}
	var all []entry
	for addr, n := range counts {
		all = append(all, entry{addr, n})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].addr < all[j].addr
	})
	if len(all) < 4 {
		t.Fatalf("access.log has only %d distinct addresses, which is too few to have a top three", len(all))
	}

	// A tie for third place would make the answer ambiguous, and the level
	// would then accept one correct answer and reject another.
	if all[2].n == all[3].n {
		t.Errorf("third and fourth place in access.log are tied on %d requests (%s and %s), "+
			"so pipe-03 has more than one correct answer and rejects all but one of them",
			all[2].n, all[2].addr, all[3].addr)
	}

	lvl := levelByID(t, "pipe-03")
	var body string
	for _, c := range walkChecks(lvl.Checks) {
		if c.ID == "top-three" {
			body, _ = c.Params["run"].(string)
		}
	}
	if body == "" {
		t.Fatal("pipe-03 has no top-three script check with a run body")
	}

	for _, e := range all[:3] {
		want := fmt.Sprintf("'%d %s'", e.n, e.addr)
		if !strings.Contains(body, want) {
			t.Errorf("access.log makes %s the busiest three, and pipe-03's check does not expect %s.\n"+
				"Recompute the expected lines from the committed log rather than adjusting the log to match.",
				"one of the top three", want)
		}
	}
}

// TestPipe04EmailsMatchTheCommittedCSV extracts and lowercases column three the
// way the level's solution does, `cut -d, -f3 | tr '[:upper:]' '[:lower:]'`.
func TestPipe04EmailsMatchTheCommittedCSV(t *testing.T) {
	var got []string
	for _, line := range assetLines(t, "assets/contacts.csv") {
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			t.Fatalf("contacts.csv line %q has %d comma separated fields, want at least 3", line, len(fields))
		}
		got = append(got, strings.ToLower(fields[2]))
	}

	declared := strings.Split(strings.TrimSpace(declaredValue(t, "pipe-04", "emails-lowercased")), "\n")
	if len(got) != len(declared) {
		t.Fatalf("contacts.csv holds %d records and pipe-04 asserts %d addresses", len(got), len(declared))
	}
	for i := range got {
		if got[i] != declared[i] {
			t.Errorf("address %d: contacts.csv gives %q and pipe-04 asserts %q.\n"+
				"The asset and the check have drifted. Recompute the answer from the committed bytes.",
				i+1, got[i], declared[i])
		}
	}
}

// TestFind01RefundLinesMatchTheCommittedTickets reproduces `grep -in refund`,
// line numbers and all.
func TestFind01RefundLinesMatchTheCommittedTickets(t *testing.T) {
	var got []string
	for i, line := range assetLines(t, "assets/support-tickets.txt") {
		if strings.Contains(strings.ToLower(line), "refund") {
			got = append(got, fmt.Sprintf("%d:%s", i+1, line))
		}
	}

	declared := strings.Split(strings.TrimSpace(declaredValue(t, "find-01", "refunds-listed")), "\n")
	if len(got) != len(declared) {
		t.Fatalf("support-tickets.txt has %d lines mentioning refund and find-01 asserts %d", len(got), len(declared))
	}
	for i := range got {
		if got[i] != declared[i] {
			t.Errorf("refund line %d:\n  asset:  %q\n  check:  %q\n"+
				"Recompute the answer from the committed bytes rather than editing the check to agree.",
				i+1, got[i], declared[i])
		}
	}
}

// TestFind01OpenCountMatchesTheCommittedTickets reproduces `grep -vc resolved`.
//
// It also asserts that "resolved" appears in one case only. If a capitalised
// spelling crept into the asset, `grep -vc resolved` and `grep -vic resolved`
// would give different answers, the level would have two defensible results,
// and it would reject one of them.
func TestFind01OpenCountMatchesTheCommittedTickets(t *testing.T) {
	lines := assetLines(t, "assets/support-tickets.txt")

	var open, openIgnoringCase int
	for _, line := range lines {
		if !strings.Contains(line, "resolved") {
			open++
		}
		if !strings.Contains(strings.ToLower(line), "resolved") {
			openIgnoringCase++
		}
	}
	if open != openIgnoringCase {
		t.Errorf("support-tickets.txt spells resolved in more than one case: %d lines lack the lower case spelling "+
			"and %d lack it in any case. find-01 would then have two correct answers and reject one of them.",
			open, openIgnoringCase)
	}

	declared := strings.TrimSpace(declaredValue(t, "find-01", "open-counted"))
	want, err := strconv.Atoi(declared)
	if err != nil {
		t.Fatalf("find-01's open-counted check declares %q, which is not a number: %v", declared, err)
	}
	if open != want {
		t.Errorf("support-tickets.txt has %d tickets that are not resolved and find-01 asserts %d.\n"+
			"Recompute the answer from the committed bytes.", open, want)
	}
}

// TestEveryAssetIsLFOnly covers the whole assets directory, not one level's
// three logs.
//
// A CRLF asset is a specific, unpleasant bug rather than a tidiness question.
// The setup runner strips the \r of a \r\n pair on materialization, so a
// carriage return in a committed file is a difference between what an author
// reads and what a learner gets. In a `.sh` it produces
// `bad interpreter: /bin/bash^M`, which reads to a beginner as though the
// sandbox is broken. .gitattributes is the first line of defence and this is
// the second, because .gitattributes only governs what git writes out.
func TestEveryAssetIsLFOnly(t *testing.T) {
	entries, err := packs.FS.ReadDir(packs.CoreLinuxBasics + "/assets")
	if err != nil {
		t.Fatalf("read the assets directory from the embedded pack: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the embedded pack has no assets, which cannot be right")
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := "assets/" + e.Name()
		data, err := packs.FS.ReadFile(packs.CoreLinuxBasics + "/" + name)
		if err != nil {
			t.Errorf("read %s: %v", name, err)
			continue
		}
		if i := strings.IndexByte(string(data), '\r'); i >= 0 {
			line := 1 + strings.Count(string(data[:i]), "\n")
			t.Errorf("%s holds a carriage return at line %d. Assets are LF only.", name, line)
		}
	}
}
