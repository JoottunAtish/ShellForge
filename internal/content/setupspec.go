package content

// Level is the typed surface a level pack decodes into. It declares only the
// four fields issue #50's setup and teardown runner reads: id, setup,
// teardown, and the source file the loader read it from.
//
// Contract with issue #53, which owns the full level type and the file names
// pack.go, level.go, check.go, load.go, embed.go, and validate.go: #53
// extends this declaration in place, in this file or after moving it to
// level.go, adding every remaining field docs/LEVEL-FORMAT.md section 2
// describes (title, act, difficulty, xp, concepts, briefing, objectives,
// checks, hints, solution, tags, and the rest). It does not redeclare Level.
// If a second declaration appears, the Go compiler reports a duplicate
// declaration immediately, which is the correct loud failure rather than a
// silent divergence between two copies of the same type. See PROGRESS.md for
// the follow-up ratification issue this decision needs against #53.
type Level struct {
	// ID is the level's unique identifier, matching [a-z0-9][a-z0-9-]* per
	// the authoring invariants in docs/LEVEL-FORMAT.md section 2. It is
	// validated with platform.ValidIdentifier before it becomes a path
	// element or an argv element, because it reaches both: the sentinel
	// path and the sandbox user allowlist.
	ID string `yaml:"id"`

	// Setup describes how the level's world is materialized into the
	// sandbox before the learner plays.
	Setup Setup `yaml:"setup"`

	// Teardown describes how the level's world is removed afterward.
	Teardown Teardown `yaml:"teardown"`

	// SourceFile is the path the level was loaded from, for use in error
	// messages. It carries yaml:"-" because it is set by the loader, never
	// by the pack's own YAML.
	SourceFile string `yaml:"-"`
}

// Setup is the "world setup" block of a level, docs/LEVEL-FORMAT.md section
// 2. Root is the level's world inside the sandbox; teardown deletes it, so
// every path this package resolves is checked against Root before it can
// reach a destructive argv.
type Setup struct {
	// Root is the level's world, an absolute path that must resolve under
	// /home/learner/. Reset is rm -rf on this path.
	Root string `yaml:"root"`

	// Files are materialized into the sandbox relative to Root.
	Files []FileSpec `yaml:"files"`

	// Script runs as root, inside the sandbox, after Files, with the
	// working directory set to the resolved Root. It is passed as a single
	// argv element to bash -c, never concatenated into a host command
	// line, and never has set -e injected: docs/LEVEL-FORMAT.md's own
	// worked examples assume the author decides that.
	Script string `yaml:"script"`

	// TimeoutSeconds bounds Script. Zero means the runner's default of 30
	// seconds.
	TimeoutSeconds int `yaml:"timeout_seconds"`
}

// FileSpec is one file to materialize inside the sandbox, relative to
// Setup.Root. Exactly one of Source, Content, and Generate must be set; the
// runner refuses a spec that sets none or more than one.
type FileSpec struct {
	// Path is relative to Setup.Root. It must not be absolute, empty, or
	// escape the root once joined and cleaned.
	Path string `yaml:"path"`

	// Source names a file inside the pack's own filesystem, checked with
	// fs.ValidPath before it is read. Mutually exclusive with Content and
	// Generate.
	Source string `yaml:"source"`

	// Content is the file body inline in the level's YAML. Mutually
	// exclusive with Source and Generate.
	Content string `yaml:"content"`

	// Generate describes a deterministic generator that produces the file
	// body. Mutually exclusive with Source and Content.
	Generate *GenerateSpec `yaml:"generate"`

	// Mode is the permission bits, as a quoted octal string such as
	// "0644". It is a string, not an integer, because docs/LEVEL-FORMAT.md
	// shows mode: "0644" quoted: an unquoted 0644 is YAML 1.1 octal, and a
	// string forces the author to be explicit. The runner parses it with
	// strconv.ParseUint(s, 8, 32); an unquoted or malformed value is a load
	// error naming the file, not a silently wrong permission bit. Empty
	// means the runner's default of 0644.
	Mode string `yaml:"mode"`

	// Owner is an owner and group pair such as "learner:learner". Empty
	// means the runner's default, also "learner:learner".
	Owner string `yaml:"owner"`
}

// GenerateSpec names a deterministic generator and its parameters. In v0.1
// the only registered kind is "loglines".
type GenerateSpec struct {
	// Kind is the registered generator name.
	Kind string `yaml:"kind"`

	// Seed makes the generator's output reproducible: the same seed
	// produces byte-identical output on every machine, with no clock and no
	// filesystem access.
	Seed int64 `yaml:"seed"`

	// Lines bounds how much the generator produces. The runner refuses a
	// value outside its registered bounds rather than truncating.
	Lines int `yaml:"lines"`
}

// Teardown is the "world setup" block's counterpart, docs/LEVEL-FORMAT.md
// section 2. It is a struct with the one field so that both `teardown: {}`
// and a block-scalar `script:` parse, matching the worked example in section
// 6 of that document.
type Teardown struct {
	// Script runs as root, inside the sandbox, with the working directory
	// /home/learner, not Setup.Root, because Root is not guaranteed to
	// exist when teardown runs. Empty means no script step.
	Script string `yaml:"script"`
}
