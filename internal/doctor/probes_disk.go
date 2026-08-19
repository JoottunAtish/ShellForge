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

func (p diskFreeProbe) Run(ctx context.Context) Result {
	if err := ctx.Err(); err != nil {
		return p.interrupted(err)
	}

	dir, err := platform.DataDir()
	if err != nil {
		return p.result(Warn, "could not resolve the data directory: "+err.Error(), "Run `shellforge doctor` again.")
	}

	_, statErr := os.Stat(dir)
	dataDirExists := statErr == nil

	target := nearestExistingAncestor(dir)
	free, err := freeBytesAt(target)
	if err != nil {
		return p.result(Warn, "could not measure free disk space at "+target+": "+err.Error(), "Run `shellforge doctor` again.")
	}

	switch classifyDiskFree(free) {
	case OK:
		detail := fmt.Sprintf("%.1f GiB free at %s", gib(free), target)
		if dataDirExists {
			return p.result(OK, detail, "No action needed.")
		}
		// Warn, not OK: Fix only considers a result whose status is not
		// OK, and the missing data directory before `shellforge init` has
		// ever run is worth fixing, not a pass.
		return p.fixableResult(Warn, detail+"; the Shellforge data directory does not exist yet",
			"Run `shellforge doctor --fix` to create it, or run `shellforge init`.")
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
