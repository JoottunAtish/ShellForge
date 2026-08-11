package docker

import (
	"archive/tar"
	"bytes"
	"os"
	"testing"
)

// writeTarFile appends one regular file entry to w, matching the tar stream
// shape `docker cp` produces on stdout, so PullFile's parsing can be tested
// without a Docker daemon.
func writeTarFile(t *testing.T, w *bytes.Buffer, name, content string) {
	t.Helper()
	tw := tar.NewWriter(w)
	hdr := &tar.Header{
		Name: name,
		Mode: 0o644,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("writeTarFile: WriteHeader: %v", err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatalf("writeTarFile: Write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("writeTarFile: Close: %v", err)
	}
}

// assertPathAbsent fails the test if path exists on the host filesystem.
func assertPathAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s still exists on the host, want it removed", path)
	} else if !os.IsNotExist(err) {
		t.Errorf("os.Stat(%s): %v, want a not-exist error", path, err)
	}
}
