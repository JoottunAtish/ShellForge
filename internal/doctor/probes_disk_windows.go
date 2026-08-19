//go:build windows

package doctor

import "golang.org/x/sys/windows"

// freeBytesAt reports the bytes available to an unprivileged caller on the
// volume holding dir, via GetDiskFreeSpaceEx.
func freeBytesAt(dir string) (uint64, error) {
	ptr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var freeAvailableToCaller, totalBytes, totalFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &freeAvailableToCaller, &totalBytes, &totalFreeBytes); err != nil {
		return 0, err
	}
	return freeAvailableToCaller, nil
}
