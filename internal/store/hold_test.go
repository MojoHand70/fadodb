package store

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// inUseBy fails unless the error says the database is held, and answers
// what it says.
func inUseBy(t *testing.T, what string, err error) string {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeInUse {
		t.Fatalf("%s: got %v, want a refusal with %s", what, err, CodeInUse)
	}
	return e.Message
}

// A program that holds a database alone keeps every other out until it
// closes it (ADR-0059, item 7).
func TestADatabaseHeldAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme.fado")
	server, err := Open(path, Options{Alone: "fadodb serve"})
	if err != nil {
		t.Fatal(err)
	}

	// A second program that opens it, to share it or to hold it, is
	// refused and told who holds it.
	_, err = Open(path, Options{})
	if got := inUseBy(t, "a program that opens the served database", err); !strings.Contains(got, "is held by fadodb serve (process ") || !strings.Contains(got, " UTC") {
		t.Errorf("the refusal says %q", got)
	}
	_, err = Open(path, Options{Alone: "fadodb user add"})
	if got := inUseBy(t, "the command-line tool on the served database", err); !strings.Contains(got, "fadodb serve") {
		t.Errorf("the refusal says %q", got)
	}

	// Closed, it is free: for a program that shares it and, after that
	// one, for a program that holds it.
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	shared, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("the database, after its server closed it: %v", err)
	}
	if err := shared.Close(); err != nil {
		t.Fatal(err)
	}
	tool, err := Open(path, Options{Alone: "fadodb key new"})
	if err != nil {
		t.Fatalf("the database, for the tool: %v", err)
	}
	if _, err := Open(path, Options{}); !strings.Contains(inUseBy(t, "a program while the tool holds the database", err), "fadodb key new") {
		t.Errorf("the refusal does not name the tool: %v", err)
	}
	tool.Close()

	// The lock file stays, and says nothing when nobody holds.
	if text, err := os.ReadFile(path + ".lock"); err != nil || len(text) != 0 {
		t.Errorf("the lock file after the last hold: %q, %v", text, err)
	}
}

// An Open that fails gives the database back.
func TestAFailedOpenHoldsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme.fado")
	if _, err := Open(path, Options{Alone: "fadodb serve", Synchronous: "SOMETIMES"}); err == nil {
		t.Fatal("an Open with a setting that is none succeeded")
	}
	s, err := Open(path, Options{Alone: "fadodb serve"})
	if err != nil {
		t.Fatalf("the database after an Open that failed: %v", err)
	}
	s.Close()
}

// A database that no program ever held alone has no lock file, and one
// that is only shared is held by nobody.
func TestADatabaseThatIsSharedIsNotHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme.fado")
	one, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("a second program that shares the database: %v", err)
	}
	two.Close()
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Errorf("a database nobody held alone has a lock file: %v", err)
	}
}

// The hold ends by itself when the program that holds it dies: here a
// second process holds the database and is killed.
func TestTheHoldEndsWithItsProgram(t *testing.T) {
	if path := os.Getenv("FADODB_TEST_HOLD"); path != "" {
		// The process that holds: it opens the database alone, says so
		// and waits to be killed.
		if _, err := Open(path, Options{Alone: "a program that will die"}); err != nil {
			os.Stdout.WriteString("failed: " + err.Error() + "\n")
			return
		}
		os.Stdout.WriteString("held\n")
		time.Sleep(time.Minute)
		return
	}
	path := filepath.Join(t.TempDir(), "acme.fado")
	holder := exec.Command(os.Args[0], "-test.run", "^TestTheHoldEndsWithItsProgram$")
	holder.Env = append(os.Environ(), "FADODB_TEST_HOLD="+path)
	out, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	defer holder.Process.Kill()
	said, _ := bufio.NewReader(out).ReadString('\n')
	if strings.TrimSpace(said) != "held" {
		t.Fatalf("the other process says %q", said)
	}
	_, err = Open(path, Options{})
	if got := inUseBy(t, "the database another process holds", err); !strings.Contains(got, "a program that will die") {
		t.Errorf("the refusal says %q", got)
	}

	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()
	// The system drops the lock as it ends the process, which may take
	// a moment.
	var s *Store
	for range 100 {
		if s, err = Open(path, Options{Alone: "fadodb serve"}); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("the database, after the program that held it was killed: %v", err)
	}
	s.Close()
}
