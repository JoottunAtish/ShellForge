package docker

import (
	"archive/tar"
	"bytes"
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
