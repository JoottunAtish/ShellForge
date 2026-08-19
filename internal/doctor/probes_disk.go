package doctor

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/JoottunAtish/ShellForge/internal/platform"
)

// minFreeBytes is the free space threshold, five gibibytes.
// docs/05-troubleshooting.md says "5 GB"; reading that figure as binary is
// the conservative direction, since five gibibytes is the larger number.
const minFreeBytes = 5 * 1024 * 1024 * 1024

// diskFreeProbe checks free disk space at internal/platform.DataDir(), or
// its nearest existing ancestor when that directory does not exist yet.
type diskFreeProbe struct {
	baseProbe
}

// diskFreeProbe.Run is about free space only, and nothing else. Whether the
// Shellforge data directory exists yet is a provisioning question, not a
// disk-space question: it is sandbox_health's row to report, since its
// sandbox-unhealthy anchor already covers a not-yet-provisioned install,
// and disk-space-low's own heading in docs/05-troubleshooting.md only ever
// talks about free space. Ample space here is OK, never a Warn, regardless
// of whether the data directory has been created yet.
func (p diskFreeProbe) Run(ctx context.Context) Result {
	if err := ctx.Err(); err != nil {
		return p.interrupted(err)
	}

	dir, err := platform.DataDir()
	if err != nil {
		return p.result(Warn, "could not resolve the data directory: "+err.Error(), "Run `shellforge doctor` again.")
	}

	target := nearestExistingAncestor(dir)
	free, err := freeBytesAt(target)
	if err != nil {
		return p.result(Warn, "could not measure free disk space at "+target+": "+err.Error(), "Run `shellforge doctor` again.")
	}

	switch classifyDiskFree(free) {
	case OK:
		return p.result(OK, fmt.Sprintf("%.1f GiB free at %s", gib(free), target), "No action needed.")
	default:
		detail := fmt.Sprintf("only %.1f GiB free at %s, need %.0f GiB", gib(free), target, gib(minFreeBytes))
		return p.result(Fail, detail, "Free up space on that drive, then run `shellforge doctor` again.")
	}
}

// gib converts a byte count to gibibytes, for display.
func gib(bytes uint64) float64 {
	return float64(bytes) / (1024 * 1024 * 1024)
}

// classifyDiskFree classifies a free-byte count against minFreeBytes.
func classifyDiskFree(free uint64) Level {
	if free >= minFreeBytes {
		return OK
	}
	return Fail
}

// freeBytesFromStatfs computes the bytes available to an unprivileged
// caller from a raw statfs result, and is the platform independent half of
// freeBytesAt: the pure arithmetic lives here, untagged, so it can be unit
// tested on any GOOS, while probes_disk_unix.go and probes_disk_windows.go
// contribute only the platform specific syscall and field widening.
//
// bavail is unix.Statfs_t.Bavail, or the Windows equivalent, widened to
// uint64. bsize is unix.Statfs_t.Bsize widened to int64: that field is
// int64 on linux/amd64, int32 on linux/386, and uint32 on darwin, so int64
// is the common type every platform's value fits into without loss.
//
// A non-positive block size is rejected rather than trusted, because a
// negative or zero Bsize converting to uint64 would otherwise report an
// enormous, wrong, number of bytes free. The multiplication is guarded
// against overflow for the same reason: silently wrapping would tell a
// learner they have plenty of space when they do not.
func freeBytesFromStatfs(bavail uint64, bsize int64) (uint64, error) {
	if bsize <= 0 {
		return 0, fmt.Errorf("statfs reported a non-positive block size (%d bytes)", bsize)
	}
	b := uint64(bsize)
	if bavail != 0 && b > math.MaxUint64/bavail {
		return 0, fmt.Errorf("statfs result overflows a 64 bit byte count: %d blocks of %d bytes", bavail, bsize)
	}
	return bavail * b, nil
}

// nearestExistingAncestor returns the nearest directory at or above dir
// that exists, so statfs has something real to measure even before
// `shellforge init` has created the data directory. It never creates
// anything.
func nearestExistingAncestor(dir string) string {
	cur := filepath.Clean(dir)
	for {
		if info, err := os.Stat(cur); err == nil && info.IsDir() {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return cur
		}
		cur = parent
	}
}
