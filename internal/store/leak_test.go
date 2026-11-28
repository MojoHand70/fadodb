package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/MojoHand70/fadodb/internal/leak"
)

// A test that leaves a database open fails on Windows; this fails it
// here too. And every database a test of this package made has its log
// checked as it closes (ADR-0103): a fault is a write the seal missed,
// or a value the check reads back otherwise than the writer sealed it.
func TestMain(m *testing.M) {
	atClose = checkSealAtClose
	code := leak.Check(m.Run())
	if faults := sealFaultsFound(); len(faults) > 0 && code == 0 {
		fmt.Fprintf(os.Stderr, "FAIL: the log of a database does not match its seal as the database closes:\n  %s\n", strings.Join(faults, "\n  "))
		code = 1
	}
	os.Exit(code)
}

// maxCheckedAtClose bounds the log a test's database is checked with as
// it closes: a benchmark's is left alone.
const maxCheckedAtClose = 20000

var sealAtClose struct {
	sync.Mutex
	faults  []string
	altered map[string]bool
}

// alteredOnPurpose says that a test changes the file behind the
// engine's back, and that the check at its close finds faults.
func alteredOnPurpose(path string) {
	sealAtClose.Lock()
	defer sealAtClose.Unlock()
	if sealAtClose.altered == nil {
		sealAtClose.altered = map[string]bool{}
	}
	sealAtClose.altered[path] = true
}

func checkSealAtClose(s *Store) {
	sealAtClose.Lock()
	skip := sealAtClose.altered[s.path]
	sealAtClose.Unlock()
	if skip {
		return
	}
	ctx := context.Background()
	var events int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM events").Scan(&events); err != nil || events > maxCheckedAtClose {
		return
	}
	check, err := s.checkLog(ctx)
	sealAtClose.Lock()
	defer sealAtClose.Unlock()
	if err != nil {
		sealAtClose.faults = append(sealAtClose.faults, s.path+": "+err.Error())
		return
	}
	for _, f := range check.Faults {
		sealAtClose.faults = append(sealAtClose.faults, fmt.Sprintf("%s: %s %s %s %s v%d (%s %s): %s", s.path, f.What, f.Event, f.Class, f.ID, f.Version, f.Kind, f.Name, f.Message))
	}
}

func sealFaultsFound() []string {
	sealAtClose.Lock()
	defer sealAtClose.Unlock()
	return sealAtClose.faults
}
