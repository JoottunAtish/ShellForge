//go:build !windows

package doctor

import (
	"context"
	"fmt"
	"strings"
)

// osVersion reports the kernel release string on every platform other than
// Windows, via `uname -r` through the runner.
func osVersion(ctx context.Context, r runner) (string, error) {
	stdout, _, code, err := r.run(ctx, []string{"uname", "-r"}, nil)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("uname -r exited %d", code)
	}
	return strings.TrimSpace(string(stdout)), nil
}
