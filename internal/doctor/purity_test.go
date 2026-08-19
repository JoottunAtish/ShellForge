package doctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/platform"
)

// hashTree walks root and folds every file's relative path, mode bits, size,
// and content digest into one sha256, so that any mutation anywhere under
// root, including one that leaves the file count unchanged, changes the
// result.
func hashTree(t *testing.T, root string) string {
	t.Helper()
	type entry struct {
		path string
		line string
	}
	var entries []entry

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		if d.IsDir() {
			entries = append(entries, entry{path: rel, line: fmt.Sprintf("dir:%s:%o", rel, info.Mode().Perm())})
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		sum := sha256.Sum256(data)
		entries = append(entries, entry{path: rel, line: fmt.Sprintf("file:%s:%o:%d:%s", rel, info.Mode().Perm(), info.Size(), hex.EncodeToString(sum[:]))})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	h := sha256.New()
	for _, e := range entries {
		h.Write([]byte(e.line))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestRunMutatesNothing is AC7. It was confirmed to fail, before this
// change was finished, against a deliberate one-line
// platform.EnsureDir(platform.DataDir()) inserted into diskFreeProbe.Run:
// the digest before and after differed and platform.DataDir() existed
// afterwards, exactly the two symptoms this test checks. That line was
// then removed. See PROGRESS.md for the confirmation record.
func TestRunMutatesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	// platform.DataDir ignores the XDG variables on Windows and reads
	// LocalAppData instead; set it too so this test isolates the same way
	// on both CI legs rather than resolving into a developer's real
	// profile.
	t.Setenv("LocalAppData", filepath.Join(home, "localappdata"))

	seedPath := filepath.Join(home, "seed.txt")
	if err := os.WriteFile(seedPath, []byte("not empty"), 0o600); err != nil {
		t.Fatalf("seed the tree: %v", err)
	}

	before := hashTree(t, home)

	fr := newFakeRunner()
	_ = run(context.Background(), fakeProber{provisioned: true, running: true}, fr, time.Second)
	_ = run(context.Background(), fakeProber{provisioned: true, running: true}, fr, time.Second)

	after := hashTree(t, home)
	if before != after {
		t.Errorf("filesystem hash changed after two calls to run:\nbefore: %s\nafter:  %s", before, after)
	}

	dataDir, err := platform.DataDir()
	if err != nil {
		t.Fatalf("platform.DataDir(): %v", err)
	}
	if _, err := os.Stat(dataDir); err == nil {
		t.Errorf("platform.DataDir() (%s) exists after run without --fix", dataDir)
	}
}
