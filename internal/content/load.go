package content

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
)

// ErrUnsupportedContentAPI is returned when a pack declares a content_api
// this build does not understand. It is a sentinel so that a caller can tell
// "this pack is from the future" apart from "this pack is malformed", which
// are different problems with different fixes.
var ErrUnsupportedContentAPI = errors.New("unsupported content_api")

// docAnchorPackInvalid is the heading in docs/05-troubleshooting.md that
// explains a pack that will not load. CI fails the build if the heading is
// missing, so this constant and that document move together.
const docAnchorPackInvalid = "level-pack-invalid"

// remediationPackInvalid is the next command to run for any pack loading
// failure. Every failure in this file names a file and, where it can, a
// field; this tells the author how to see all of them at once instead of one
// per run.
const remediationPackInvalid = "The message above names the file that failed to load. Fix it, then check the whole pack at once with `shellforge author validate packs/core-linux-basics`."

// levelFileExtensions are the file extensions the loader reads out of a
// pack's levels/ directory.
//
// Both spellings are accepted deliberately. Accepting only .yaml would make a
// level saved as .yml disappear from the campaign without a word, and a level
// that silently does not exist is far harder to diagnose than one that fails
// to parse.
var levelFileExtensions = map[string]bool{".yaml": true, ".yml": true}

// LoadPack reads a content pack rooted at root inside fsys and returns it
// with every level file under levels/ decoded into Levels.
//
// Decoding is strict: a key the schema does not define is an error naming the
// file, not a silently ignored typo. LoadPack does not judge whether the pack
// is internally consistent, which is Validate's job; it reports only what
// stopped it from producing typed values at all.
//
// A pack whose levels/ directory is missing or holds no level file loads
// clean with zero levels. That is the normal state of a pack being filled in,
// and refusing it would mean a pack could not be validated until it was
// finished.
func LoadPack(fsys fs.FS, root string) (*Pack, error) {
	pack, err := loadPackFile(fsys, root)
	if err != nil {
		return nil, err
	}

	levels, err := loadLevelFiles(fsys, root)
	if err != nil {
		return nil, err
	}
	pack.Levels = levels

	return pack, nil
}

// loadPackFile reads and decodes pack.yaml.
func loadPackFile(fsys fs.FS, root string) (*Pack, error) {
	name := joinPack(root, "pack.yaml")

	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, ux.Fail("read the content pack", fmt.Errorf("read %s: %w", name, err),
			"Check that the path you gave names a pack directory: it must contain a pack.yaml and a levels/ directory. See docs/LEVEL-FORMAT.md section 1.",
			docAnchorPackInvalid)
	}

	var pack Pack
	if err := Unmarshal(name, data, &pack); err != nil {
		return nil, ux.Fail("read the content pack", err, remediationPackInvalid, docAnchorPackInvalid)
	}

	if pack.ContentAPI != ContentAPIVersion {
		return nil, ux.Fail("read the content pack",
			fmt.Errorf("%s declares content_api %d, this build understands %d: %w", name, pack.ContentAPI, ContentAPIVersion, ErrUnsupportedContentAPI),
			"A pack written for a newer level format cannot be read by this build. Update Shellforge, or use a pack whose content_api is "+fmt.Sprint(ContentAPIVersion)+".",
			docAnchorPackInvalid)
	}

	return &pack, nil
}

// loadLevelFiles reads every level file under root/levels, in filename order
// so that a pack loads identically on every machine regardless of how the
// filesystem happens to order its entries.
func loadLevelFiles(fsys fs.FS, root string) ([]Level, error) {
	dir := joinPack(root, "levels")

	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil // a pack with no levels/ directory yet is not an error
		}
		return nil, ux.Fail("read the content pack", fmt.Errorf("read %s: %w", dir, err),
			"Check that the pack's levels/ directory exists and is readable.",
			docAnchorPackInvalid)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !levelFileExtensions[strings.ToLower(path.Ext(entry.Name()))] {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	levels := make([]Level, 0, len(names))
	for _, name := range names {
		relative := path.Join("levels", name)

		data, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, ux.Fail("read the level definition", fmt.Errorf("read %s: %w", relative, err),
				remediationPackInvalid, docAnchorPackInvalid)
		}

		var level Level
		if err := Unmarshal(relative, data, &level); err != nil {
			return nil, ux.Fail("read the level definition", err, remediationPackInvalid, docAnchorPackInvalid)
		}
		level.SourceFile = relative

		levels = append(levels, level)
	}

	return levels, nil
}

// joinPack joins a pack-relative name onto root, producing a path fs.FS will
// accept. It exists because a root of "." must produce "pack.yaml" and not
// "./pack.yaml": fs.ValidPath rejects the second, and callers legitimately
// pass "." when the pack directory is itself the filesystem root.
func joinPack(root, name string) string {
	if root == "" || root == "." {
		return name
	}
	return path.Join(root, name)
}
