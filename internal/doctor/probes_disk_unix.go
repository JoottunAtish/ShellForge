//go:build !windows

package doctor

import "golang.org/x/sys/unix"

// freeBytesAt reports the bytes available to an unprivileged caller on the
// filesystem holding dir, via statfs. It uses Bavail, the
// available-to-unprivileged-users count, rather than Bfree, the total free
// count, so this does not overstate what a learner's own user can actually
// use.
func freeBytesAt(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
