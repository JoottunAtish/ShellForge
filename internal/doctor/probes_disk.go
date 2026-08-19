package doctor

import (
	"context"
	"fmt"
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
