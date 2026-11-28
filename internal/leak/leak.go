// Package leak fails the tests of a package when one of them left a
// database open.
//
// Windows refuses to remove a file that is open. A test that opens a
// database in its temporary directory and forgets to close it passes on
// Linux and on macOS and fails on Windows, in the cleanup of the
// directory, after the push. This package says so on Linux, where the
// tests are written: a package's tests call Main from their TestMain,
// and when they have all run, a database file the process still holds
// open fails them, with its path, which carries the name of the test
// that opened it.
//
//	func TestMain(m *testing.M) { leak.Main(m) }
package leak

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Main runs the tests of a package and ends the test program. It fails
// the tests when they passed and a database is still open.
func Main(m *testing.M) {
	os.Exit(Check(m.Run()))
}

// Check is the exit code of tests that ended with the code given: 1
// when they passed and a database is still open. A TestMain that checks
// more after the tests calls it in place of Main.
func Check(code int) int {
	if left := Open(); len(left) > 0 && code == 0 {
		fmt.Fprintf(os.Stderr, "FAIL: a test left a database open, which fails the tests on Windows, where an open file is not removed. Close it, as with defer db.Close():\n  %s\n", strings.Join(left, "\n  "))
		code = 1
	}
	return code
}

// Open lists the files of databases that this process holds open: the
// database itself and the two files beside it. It asks the system
// through /proc, and answers nothing where there is none.
func Open() []string {
	links, _ := filepath.Glob("/proc/self/fd/*")
	seen := map[string]bool{}
	var out []string
	for _, link := range links {
		file, err := os.Readlink(link)
		if err != nil || !strings.Contains(filepath.Base(file), ".fado") || seen[file] {
			continue
		}
		seen[file] = true
		out = append(out, file)
	}
	sort.Strings(out)
	return out
}
