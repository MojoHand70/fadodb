//go:build windows

package store

import (
	"os"
	"syscall"
	"unsafe"
)

// LockFileEx of kernel32, which the syscall package does not wrap.
var procLockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

const (
	lockfileFailImmediately = 0x1
	lockfileExclusiveLock   = 0x2
	// lockByte is the byte of the file that is locked: one far past
	// what the file holds, so that who holds the database can be read
	// while it is held.
	lockByte = 1 << 30
)

// lockFile locks a file for this program, alone or together with others
// that do not want it alone, and fails at once when it cannot.
func lockFile(f *os.File, alone bool) error {
	flags := uintptr(lockfileFailImmediately)
	if alone {
		flags |= lockfileExclusiveLock
	}
	var at syscall.Overlapped
	at.Offset = lockByte
	ok, _, err := procLockFileEx.Call(f.Fd(), flags, 0, 1, 0, uintptr(unsafe.Pointer(&at)))
	if ok == 0 {
		return err
	}
	return nil
}
