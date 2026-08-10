package platform_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform"
)

func TestConfigDir(t *testing.T) {
	dir, err := platform.ConfigDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(dir, "shellforge") {
		t.Errorf("expected path ending in shellforge, got %q", dir)
	}
}

func TestCacheDir(t *testing.T) {
	dir, err := platform.CacheDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(dir, "shellforge") {
		t.Errorf("expected path ending in shellforge, got %q", dir)
	}
}

func TestDataDir(t *testing.T) {
	t.Run("XDG_DATA_HOME set", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("XDG_DATA_HOME", tmpDir)

		dir, err := platform.DataDir()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := filepath.Join(tmpDir, "shellforge")
		if dir != expected {
			t.Errorf("expected %q, got %q", expected, dir)
		}
	})
}

func TestLogDir(t *testing.T) {
	dir, err := platform.LogDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(dir, filepath.Join("shellforge", "logs")) {
		t.Errorf("expected path ending in shellforge/logs, got %q", dir)
	}
}

func TestDatabasePath(t *testing.T) {
	dbPath, err := platform.DatabasePath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(dbPath, filepath.Join("shellforge", "progress.db")) {
		t.Errorf("expected path ending in progress.db, got %q", dbPath)
	}
}

func TestEnsureDir(t *testing.T) {
	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "sub", "dir")

	err := platform.EnsureDir(targetDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(targetDir)
	if err != nil {
		t.Fatalf("expected directory to exist: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("expected %q to be a directory", targetDir)
	}
}
