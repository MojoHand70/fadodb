//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly || windows)

package store

import "os"

// lockFile locks nothing on a system the engine has no lock for: a
// database is then held alone by nobody, as before ADR-0059.
func lockFile(*os.File, bool) error { return nil }
