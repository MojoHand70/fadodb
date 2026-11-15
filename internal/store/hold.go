package store

import (
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// A database that is served is held alone (ADR-0059, item 7): the
// server keeps in memory what it has read of the database, its classes,
// its users and their keys, so a second program that wrote to the same
// database would change it behind the server's back. A program that
// opens a database with Options.Alone holds it until it closes it; while
// it does, every other Open of that database is refused with in_use and
// told who holds it.
//
// The hold is a lock of the operating system on a file beside the
// database, <path>.lock. The system drops it when the program ends,
// however it ends, so a hold never outlives its program. The file stays
// when the hold is gone: a lock file that is removed can be locked twice.

// hold is the hold of this program on a database.
type hold struct{ f *os.File }

// holdTries and holdPause are how often and how far apart a program
// that wants a database alone asks for it. Another program that is only
// looking whether the database is free holds the lock for a moment.
const (
	holdTries = 5
	holdPause = 20 * time.Millisecond
)

// holdAlone takes a database for this program alone, under the name the
// program gives itself, or refuses with in_use when another program
// holds it.
func holdAlone(path, holder string, now time.Time) (*hold, error) {
	f, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, storageErr("open the lock of the database", err)
	}
	for try := 0; ; try++ {
		err = lockFile(f, true)
		if err == nil {
			break
		}
		if try == holdTries-1 {
			by := heldBy(f)
			f.Close()
			return nil, inUse(path, by)
		}
		time.Sleep(holdPause)
	}
	// Whoever is refused is told who holds the database.
	line := holder + " (process " + strconv.Itoa(os.Getpid()) + ") since " + now.UTC().Format("2006-01-02 15:04:05") + " UTC\n"
	err = f.Truncate(0)
	if err == nil {
		_, err = f.WriteAt([]byte(line), 0)
	}
	if err != nil {
		f.Close()
		return nil, storageErr("write the lock of the database", err)
	}
	return &hold{f: f}, nil
}

// heldAlone refuses a database that another program holds alone. It
// holds nothing itself: the lock is taken to look, and given back.
func heldAlone(path string) error {
	f, err := os.Open(path + ".lock")
	if err != nil {
		// No lock file: no program ever held the database alone.
		return nil
	}
	defer f.Close()
	if err := lockFile(f, false); err != nil {
		return inUse(path, heldBy(f))
	}
	return nil
}

// release gives the database back.
func (h *hold) release() {
	if h == nil || h.f == nil {
		return
	}
	_ = h.f.Truncate(0)
	_ = h.f.Close()
	h.f = nil
}

// heldBy reads who holds the database, as the holder wrote it.
func heldBy(f *os.File) string {
	text := make([]byte, 256)
	n, err := f.ReadAt(text, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	line, _, _ := strings.Cut(string(text[:n]), "\n")
	return strings.TrimSpace(line)
}

func inUse(path, by string) *Error {
	message := "the database " + path + " is held by another program"
	if by != "" {
		message = "the database " + path + " is held by " + by
	}
	return hintf(CodeInUse, "", message,
		"a database that is served is reached through its server, with a key; to open it on the machine, stop the program that holds it")
}
