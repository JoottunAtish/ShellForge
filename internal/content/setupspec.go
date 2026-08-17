package content

import (
	"fmt"

	"github.com/goccy/go-yaml/ast"
)

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

	// ContentSet reports whether the YAML declared a content key at all,
	// which is not the same question as whether Content is empty.
	//
	// An empty file is a real thing a level needs to materialize: files-01
	// creates a .gitkeep, which exists precisely to be empty. Deciding "did
	// the author choose content" by testing Content != "" makes that level
	// unwritable, because an empty body and an absent key look identical.
	// The decoder below records the answer instead of inferring it. Issue
	// #93 is what required this field to be declared explicitly rather than
	// left as a comment on the decoder.
	//
	// It carries yaml:"-" because it is set by the decoder, never by the
	// pack's own YAML.
	ContentSet bool `yaml:"-"`

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

// fileSpecKeys are the keys a setup file entry may declare. The decoder
// refuses anything else, which keeps a typo a rejection rather than a
// silently missing mode or owner.
var fileSpecKeys = map[string]bool{
	"path":     true,
	"source":   true,
	"content":  true,
	"generate": true,
	"mode":     true,
	"owner":    true,
}

// UnmarshalYAML decodes one setup file entry.
//
// It is hand-rolled for one reason: ContentSet. Struct tag decoding cannot
// tell an absent content key from an empty one, and a level that materializes
// an empty file needs that distinction. Everything else here is what strict
// struct decoding would have done anyway, including refusing an unknown key.
func (f *FileSpec) UnmarshalYAML(node ast.Node) error {
	*f = FileSpec{}

	m, ok := node.(ast.MapNode)
	if !ok {
		return fmt.Errorf("a setup file entry must be a mapping of fields, found %s", node.Type())
	}

	seen := map[string]bool{}
	for it := m.MapRange(); it.Next(); {
		key := it.Key().GetToken().Value
		if !fileSpecKeys[key] {
			return fmt.Errorf("unknown setup file field %q", key)
		}
		if seen[key] {
			return fmt.Errorf("setup file field %q appears twice", key)
		}
		seen[key] = true

		var err error
		switch key {
		case "path":
			err = decodeNode(it.Value(), key, &f.Path)
		case "source":
			err = decodeNode(it.Value(), key, &f.Source)
		case "content":
			err = decodeNode(it.Value(), key, &f.Content)
			f.ContentSet = true
		case "generate":
			var g GenerateSpec
			if err = decodeNode(it.Value(), key, &g); err == nil {
				f.Generate = &g
			}
		case "mode":
			err = decodeNode(it.Value(), key, &f.Mode)
		case "owner":
			err = decodeNode(it.Value(), key, &f.Owner)
		}
		if err != nil {
			return err
		}
	}
	return nil
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
