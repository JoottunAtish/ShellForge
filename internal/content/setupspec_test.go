package content

import "testing"

// TestLevelDecodesTheSetupSchemaBlock round-trips the setup and teardown
// shapes from docs/LEVEL-FORMAT.md section 2, including a source file, an
// inline content file, a generated file, and a multi-line script.
func TestLevelDecodesTheSetupSchemaBlock(t *testing.T) {
	data := []byte(`
id: pipe-05
setup:
  root: /home/learner/quest
  files:
    - path: logs/app-1.log
      source: assets/app-1.log
      mode: "0644"
      owner: "learner:learner"
    - path: notes.txt
      content: |
        Kofi was here.
    - path: data/big.log
      generate:
        kind: loglines
        seed: 91731
        lines: 4000
  script: |
    touch -d "2 days ago" logs/app-1.log
    chown -R learner:learner .
  timeout_seconds: 30
teardown:
  script: |
    pkill -f atlas-indexer || true
`)

	var lvl Level
	if err := Unmarshal("nav-01.yaml", data, &lvl); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if lvl.ID != "pipe-05" {
		t.Errorf("ID = %q, want pipe-05", lvl.ID)
	}
	if lvl.Setup.Root != "/home/learner/quest" {
		t.Errorf("Setup.Root = %q, want /home/learner/quest", lvl.Setup.Root)
	}
	if len(lvl.Setup.Files) != 3 {
		t.Fatalf("len(Setup.Files) = %d, want 3", len(lvl.Setup.Files))
	}

	sourced := lvl.Setup.Files[0]
	if sourced.Path != "logs/app-1.log" || sourced.Source != "assets/app-1.log" {
		t.Errorf("files[0] = %+v, want a sourced file spec", sourced)
	}
	if sourced.Mode != "0644" {
		t.Errorf("files[0].Mode = %q, want the quoted string \"0644\"", sourced.Mode)
	}
	if sourced.Owner != "learner:learner" {
		t.Errorf("files[0].Owner = %q, want learner:learner", sourced.Owner)
	}

	inline := lvl.Setup.Files[1]
	if inline.Path != "notes.txt" || inline.Content != "Kofi was here.\n" {
		t.Errorf("files[1] = %+v, want an inline content file spec", inline)
	}

	generated := lvl.Setup.Files[2]
	if generated.Generate == nil {
		t.Fatal("files[2].Generate is nil, want a GenerateSpec")
	}
	if generated.Generate.Kind != "loglines" || generated.Generate.Seed != 91731 || generated.Generate.Lines != 4000 {
		t.Errorf("files[2].Generate = %+v, want {loglines 91731 4000}", generated.Generate)
	}

	if lvl.Setup.TimeoutSeconds != 30 {
		t.Errorf("Setup.TimeoutSeconds = %d, want 30", lvl.Setup.TimeoutSeconds)
	}
	if lvl.Setup.Script == "" {
		t.Error("Setup.Script is empty, want the two touch/chown lines")
	}
	if lvl.Teardown.Script == "" {
		t.Error("Teardown.Script is empty, want the pkill line")
	}
}

// TestLevelDecodesTheWorkedExampleSetupBlock round-trips the setup and
// teardown shapes from docs/LEVEL-FORMAT.md section 6's worked example,
// including the flow-mapping file entries, a plain-scalar script, and an
// empty teardown: {}.
func TestLevelDecodesTheWorkedExampleSetupBlock(t *testing.T) {
	data := []byte(`
id: pipe-05
setup:
  root: /home/learner/quest
  files:
    - { path: logs/app-1.log, source: assets/app-1.log }
    - { path: logs/app-2.log, source: assets/app-2.log }
    - { path: logs/billing.log, source: assets/billing.log }
  script: chown -R learner:learner .
teardown: {}
`)

	var lvl Level
	if err := Unmarshal("pipe-05.yaml", data, &lvl); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(lvl.Setup.Files) != 3 {
		t.Fatalf("len(Setup.Files) = %d, want 3", len(lvl.Setup.Files))
	}
	for i, f := range lvl.Setup.Files {
		if f.Path == "" || f.Source == "" {
			t.Errorf("files[%d] = %+v, want both path and source set", i, f)
		}
	}
	if lvl.Setup.Script != "chown -R learner:learner ." {
		t.Errorf("Setup.Script = %q, want the plain scalar chown line", lvl.Setup.Script)
	}
	if lvl.Teardown.Script != "" {
		t.Errorf("Teardown.Script = %q, want empty for teardown: {}", lvl.Teardown.Script)
	}
}

// TestLevelSourceFileIsNotAYAMLField pins that SourceFile is set by the
// loader, not decoded from the pack: a pack that tried to set it would hit
// strict mode's unknown-field rejection, since the tag is "-".
func TestLevelSourceFileIsNotAYAMLField(t *testing.T) {
	var lvl Level
	err := Unmarshal("nav-01.yaml", []byte("id: nav-01\nsourcefile: nav-01.yaml\n"), &lvl)
	if err == nil {
		t.Fatal("Unmarshal silently accepted a sourcefile key, want strict mode to reject it")
	}
}
