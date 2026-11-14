//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package store

import (
	"os"
	"syscall"
)

// lockFile locks a file for this program, alone or together with others
// that do not want it alone, and fails at once when it cannot.
func lockFile(f *os.File, alone bool) error {
	how := syscall.LOCK_SH
	if alone {
		how = syscall.LOCK_EX
	}
	for {
		err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
		if err != syscall.EINTR {
			return err
		}
	}
}
