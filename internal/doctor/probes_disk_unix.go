//go:build !windows

package doctor

import "golang.org/x/sys/unix"

// freeBytesAt reports the bytes available to an unprivileged caller on the
// filesystem holding dir, via statfs. It uses Bavail, the
// available-to-unprivileged-users count, rather than Bfree, the total free
// count, so this does not overstate what a learner's own user can actually
// use.
//
// Bavail is uint64 on every unix platform this builds for. Bsize is not:
// it is int64 on linux/amd64, int32 on linux/386, and uint32 on darwin.
// Widening it to int64 here, rather than straight to uint64, is what lets
// freeBytesFromStatfs reject a negative Bsize instead of silently turning
// it into a huge unsigned number.
func freeBytesAt(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return freeBytesFromStatfs(uint64(st.Bavail), int64(st.Bsize))
}
