//go:build windows

package doctor

import (
	"context"
	"strconv"

	"golang.org/x/sys/windows"
)

// osVersion reports the Windows build number, as a decimal string, via
// RtlGetVersion. It needs no subprocess and is not lied to by the
// compatibility shims that GetVersionEx is subject to. r is accepted for
// signature parity with the non-Windows implementation; it is unused here.
func osVersion(ctx context.Context, r runner) (string, error) {
	v := windows.RtlGetVersion()
	return strconv.FormatUint(uint64(v.BuildNumber), 10), nil
}
