package verify

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"
)

// section5Document is the JSON from docs/LEVEL-FORMAT.md section 5, reproduced
// here as the thing this package's types are checked against.
//
// It is a copy, which is a real cost: the document and this string can drift.
// The alternative, parsing the fenced block out of the Markdown at test time,
// was considered and rejected. internal/verify may not read the level format
// document to decide anything at run time, and a test that greps a Markdown
// fence is a test of the fence's position in the file: moving section 5 down a
// paragraph would break it for no reason a reader could see. A copy with the
// section named in a comment is the honest version of the same assertion.
//
// The `score` object from the document is deliberately absent. See the doc
// comment on LevelResult for why that is a layer boundary rather than a gap.
const section5Document = `{
  "level_id": "pipe-05",
  "passed": false,
  "objectives": [
    {"id":"obj1","text":"report.txt holds the total ERROR count","status":"pass"},
    {"id":"obj2","text":"codes.txt lists the distinct error codes, sorted","status":"fail",
     "message":"codes.txt should hold the distinct codes, one per line, sorted."},
    {"id":"obj3","text":"Counted with a single pipeline","status":"fail","optional":true,
     "message":"You got there - but there's a one-liner."}
  ],
  "primary_failure": {"id":"obj2","text":"codes.txt lists the distinct error codes, sorted",
                      "status":"fail",
                      "message":"codes.txt should hold the distinct codes, one per line, sorted."},
  "notes": ["You got there - but there's a one-liner.",
            "Hardcoding the answer works, but you won't learn the pipeline."],
  "duration_ms": 412
}`

// TestLevelResultMarshalsToLevelFormatSection5 asserts the published shape,
// key by key, against the document.
//
// It compares key sets rather than whole documents. Comparing bytes would make
// the test fail on key order and on whitespace, neither of which is part of the
// contract, and would say nothing useful when it did fail.
func TestLevelResultMarshalsToLevelFormatSection5(t *testing.T) {
	var want map[string]any
	if err := json.Unmarshal([]byte(section5Document), &want); err != nil {
		t.Fatalf("the section 5 document in this test is not valid JSON: %v", err)
	}

	const codesOnFail = "codes.txt should hold the distinct codes, one per line, sorted."
	const codesText = "codes.txt lists the distinct error codes, sorted"
	const onelinerOnFail = "You got there - but there's a one-liner."

	got := marshalToMap(t, LevelResult{
		LevelID: "pipe-05",
		Passed:  false,
		Objectives: []ObjectiveResult{
			{ID: "obj1", Text: "report.txt holds the total ERROR count", Status: StatusPass},
			{ID: "obj2", Text: codesText, Status: StatusFail, Message: codesOnFail},
			{ID: "obj3", Text: "Counted with a single pipeline", Status: StatusFail, Optional: true,
				Message: onelinerOnFail},
		},
		PrimaryFailure: &ObjectiveResult{ID: "obj2", Text: codesText, Status: StatusFail, Message: codesOnFail},
		Notes: []string{
			onelinerOnFail,
			"Hardcoding the answer works, but you won't learn the pipeline.",
		},
		DurationMS: 412,
	})

	assertSameKeys(t, "the top level", want, got)

	// score must be absent, not null and not zero. A caller distinguishing
	// "the engine does not score" from "the engine scored zero" reads the
	// key's presence, so an accidental `json:"score"` on a zero value would be
	// a silent behaviour change.
	if _, present := got["score"]; present {
		t.Error(`marshalled a "score" key; scoring belongs to internal/game on Day 4 and this layer must not publish the field`)
	}

	// primary_failure repeats a whole objective rather than referring to one by
	// id, so its key set is part of the contract too. The document used to
	// elide it as {"id":..., "message":"..."}, which read as a narrower type
	// than it is.
	assertSameKeys(t, "primary_failure", nestedObject(t, want, "primary_failure"), nestedObject(t, got, "primary_failure"))

	wantObjectives := objectiveMaps(t, want)
	gotObjectives := objectiveMaps(t, got)
	if len(wantObjectives) != len(gotObjectives) {
		t.Fatalf("marshalled %d objectives, the document has %d", len(gotObjectives), len(wantObjectives))
	}
	for i := range wantObjectives {
		assertSameKeys(t, "objectives["+itoa(i)+"]", wantObjectives[i], gotObjectives[i])
	}
}

// TestObjectiveResultOmitsOptionalAndMessageWhenEmpty pins the two omitempty
// tags. Section 5 shows `optional` only on the bonus objective and `message`
// only where there is one, so a passing objective that published
// `"optional":false,"message":""` would not match the document even though
// every value in it is correct.
func TestObjectiveResultOmitsOptionalAndMessageWhenEmpty(t *testing.T) {
	got := marshalToMap(t, LevelResult{
		Objectives: []ObjectiveResult{{ID: "obj1", Text: "a thing", Status: StatusPass}},
		Notes:      []string{},
	})

	objectives := objectiveMaps(t, got)
	if len(objectives) != 1 {
		t.Fatalf("got %d objectives, want 1", len(objectives))
	}
	for _, key := range []string{"optional", "message"} {
		if _, present := objectives[0][key]; present {
			t.Errorf("a passing objective published %q; section 5 shows it only where it carries information", key)
		}
	}

	// primary_failure is omitempty too, and nil is the normal case for a
	// level that passed.
	if _, present := got["primary_failure"]; present {
		t.Error(`published "primary_failure" for a result with none; the tag is omitempty so nil must not appear`)
	}
}

// TestLevelResultMarshalsEmptySlicesAsArrays is the null-versus-empty
// assertion. A zero LevelResult has nil slices, and Go marshals a nil slice as
// null, so this only holds because the engine always allocates. The test is
// here rather than in engine_test.go because the promise is about the published
// document, not about how the engine happens to build it.
func TestLevelResultMarshalsEmptySlicesAsArrays(t *testing.T) {
	raw, err := json.Marshal(LevelResult{Objectives: []ObjectiveResult{}, Notes: []string{}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, want := range []string{`"objectives":[]`, `"notes":[]`} {
		if !strings.Contains(body, want) {
			t.Errorf("marshalled document does not contain %s:\n%s", want, body)
		}
	}

	// And the failure this guards against, stated as its own assertion so the
	// reason the engine allocates is written down somewhere executable.
	nullish, err := json.Marshal(LevelResult{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(nullish), `"objectives":null`) {
		t.Fatal("a nil Objectives slice no longer marshals as null; this test's premise is stale and the engine's allocation may no longer be load bearing")
	}
}

// TestObjectiveBlockingIgnoresOptionalAndWarn documents the rule Passed rests
// on, at the level of the one predicate that decides it.
func TestObjectiveBlockingIgnoresOptionalAndWarn(t *testing.T) {
	tests := []struct {
		name string
		obj  ObjectiveResult
		want bool
	}{
		{name: "a required failure blocks", obj: ObjectiveResult{Status: StatusFail}, want: true},
		{name: "a required pass blocks", obj: ObjectiveResult{Status: StatusPass}, want: true},
		{name: "a required timeout blocks", obj: ObjectiveResult{Status: StatusTimeout}, want: true},
		{name: "a required error blocks", obj: ObjectiveResult{Status: StatusError}, want: true},
		{name: "an optional failure does not block", obj: ObjectiveResult{Status: StatusFail, Optional: true}, want: false},
		{name: "a warn does not block", obj: ObjectiveResult{Status: StatusWarn}, want: false},
		{name: "an optional warn does not block", obj: ObjectiveResult{Status: StatusWarn, Optional: true}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.obj.blocking(); got != tt.want {
				t.Errorf("blocking() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDurationMSIsMilliseconds guards the unit, which a reader cannot see from
// the type: an int64 holding a time.Duration would marshal as nanoseconds and
// the field name would still read correctly.
func TestDurationMSIsMilliseconds(t *testing.T) {
	res := LevelResult{DurationMS: (1500 * time.Millisecond).Milliseconds()}
	if res.DurationMS != 1500 {
		t.Errorf("DurationMS = %d for 1.5s, want 1500: the field is milliseconds, not nanoseconds", res.DurationMS)
	}
}

// marshalToMap marshals v and unmarshals it into a map, which is how these
// tests inspect the published shape without depending on key order.
func marshalToMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal what we just marshalled: %v", err)
	}
	return out
}

// nestedObject pulls one object-valued key out of a marshalled document.
func nestedObject(t *testing.T, doc map[string]any, key string) map[string]any {
	t.Helper()
	raw, ok := doc[key]
	if !ok {
		t.Fatalf("document has no %q key", key)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("%q is %T, want an object", key, raw)
	}
	return m
}

// objectiveMaps pulls the objectives array out of a marshalled document.
func objectiveMaps(t *testing.T, doc map[string]any) []map[string]any {
	t.Helper()
	raw, ok := doc["objectives"]
	if !ok {
		t.Fatal(`document has no "objectives" key`)
	}
	items, ok := raw.([]any)
	if !ok {
		t.Fatalf(`"objectives" is %T, want an array`, raw)
	}
	out := make([]map[string]any, 0, len(items))
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("objectives[%d] is %T, want an object", i, item)
		}
		out = append(out, m)
	}
	return out
}

// assertSameKeys reports every key present in one map and not the other,
// naming which side is missing it, so a failure says what to change.
func assertSameKeys(t *testing.T, where string, want, got map[string]any) {
	t.Helper()
	for _, key := range sortedKeys(want) {
		if _, present := got[key]; !present {
			t.Errorf("%s: docs/LEVEL-FORMAT.md section 5 has key %q and the marshalled result does not", where, key)
		}
	}
	for _, key := range sortedKeys(got) {
		if _, present := want[key]; !present {
			t.Errorf("%s: the marshalled result has key %q and docs/LEVEL-FORMAT.md section 5 does not; update the document in the same commit or drop the field", where, key)
		}
	}
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// itoa avoids pulling strconv in for one call in a test message.
func itoa(i int) string {
	return string(rune('0' + i))
}
