//go:build windows

package filestore

import (
	"syscall"
	"unsafe"
)

const moveFileReplaceExisting = 0x1
const moveFileWriteThrough = 0x8

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var moveFileExW = kernel32.NewProc("MoveFileExW")

func atomicReplace(src, dst string) error {
	from, err := syscall.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	r1, _, callErr := moveFileExW.Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), uintptr(moveFileReplaceExisting|moveFileWriteThrough))
	if r1 == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return callErr
		}
		return syscall.EINVAL
	}
	return nil
}

// MoveFileEx with WRITE_THROUGH flushes the move. Opening directories for fsync
// is not portable on Windows, so no second directory handle is used here.
func syncDirectory(string) error { return nil }
