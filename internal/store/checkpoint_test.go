package store

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A note, for the tests of the checkpointer: a small commit.
const logNote = `{"name": "Note", "variables": {"text": {"type": "text"}}, "states": {"initial": "new", "list": ["new"]}}`

// writing opens a store whose log is emptied past limit bytes, and
// starts writers that commit notes without a pause. commits counts what
// they have committed; stop ends them.
func writing(t *testing.T, limit int64, writers int) (s *Store, path string, commits *atomic.Int64, stop func()) {
	t.Helper()
	s, path = openStore(t, Options{WALLimit: limit, CheckpointEvery: 5 * time.Millisecond})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(logNote), asSetup); err != nil {
		t.Fatal(err)
	}
	var ended atomic.Bool
	var wg sync.WaitGroup
	commits = &atomic.Int64{}
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !ended.Load() {
				if _, err := s.Create(ctx, "Note", Values{"text": "a note of some length, as a caller writes one"}, asSetup); err != nil {
					// The store was closed under the writer, which one test
					// does on purpose.
					if code(err) != CodeClosed {
						t.Error(err)
					}
					return
				}
				commits.Add(1)
			}
		}()
	}
	var once sync.Once
	stop = func() {
		once.Do(func() {
			ended.Store(true)
			wg.Wait()
		})
	}
	t.Cleanup(stop)
	return s, path, commits, stop
}

// logSize is the size of the log beside a database, 0 when there is
// none.
func logSize(path string) int64 {
	fi, err := os.Stat(path + "-wal")
	if err != nil {
		return 0
	}
	return fi.Size()
}

// emptied watches the log for a while and answers how often it was
// emptied, which shows as a log less than half the size it had a
// millisecond before, and how large it was at its largest.
func emptied(path string, long time.Duration) (times int, largest int64) {
	last := logSize(path)
	for until := time.Now().Add(long); time.Now().Before(until); time.Sleep(time.Millisecond) {
		size := logSize(path)
		if size < last/2 {
			times++
		}
		largest, last = max(largest, size), size
	}
	return times, largest
}

// A writer that never pauses leaves no moment at which the log could be
// emptied, and the log of such a writer grew to gigabytes. The
// checkpointer takes its turn among the commits, so the log is emptied
// while the writers write and stays near its limit (ADR-0035, item 7).
func TestTheLogStaysNearItsLimit(t *testing.T) {
	const limit = 1 << 20
	_, path, _, stop := writing(t, limit, 4)
	// Watched while the writers write: they leave no pause.
	times, largest := emptied(path, 1500*time.Millisecond)
	stop()
	// How often depends on how fast the machine writes; that it happens
	// at all while the writers write, and that the log stays near its
	// limit, does not. Without the turn the log of this test grew to
	// 600 times its limit.
	if times == 0 {
		t.Errorf("the log was never emptied in a second and a half of writing without a pause")
	}
	if largest > 64*limit {
		t.Errorf("the log grew to %d MiB under a limit of %d MiB", largest>>20, limit>>20)
	}
	t.Logf("the log was emptied %d times and was %d KiB at its largest, under a limit of %d KiB", times, largest>>10, limit>>10)
}

// A reader that still looks at an old view of the database keeps the
// log from being emptied. The checkpointer does not wait for the reader
// in the writer's turn, where every commit would wait with it: the
// commits go on, and the log is emptied when the reader is done.
func TestAReaderDoesNotHoldTheWriters(t *testing.T) {
	const limit = 256 << 10
	s, path, commits, stop := writing(t, limit, 2)
	ctx := context.Background()
	// A reader with a view of its own, kept open.
	reader, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := reader.QueryRowContext(ctx, "SELECT count(*) FROM outbox").Scan(&n); err != nil {
		t.Fatal(err)
	}
	// While the reader keeps its view, the writers commit.
	time.Sleep(100 * time.Millisecond)
	before := commits.Load()
	time.Sleep(500 * time.Millisecond)
	// A checkpointer that waited for the reader in the writer's turn
	// would let no commit through for as long as it waits: the test saw
	// none. A slow machine with the race detector makes a few hundred.
	if made := commits.Load() - before; made < 20 {
		t.Errorf("%d commits in half a second while a reader kept its view: the checkpointer held the writers for the reader", made)
	}
	if err := reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	// The reader gone, the log is emptied again, with the writers still
	// writing.
	for until := time.Now().Add(10 * time.Second); ; {
		if times, _ := emptied(path, 50*time.Millisecond); times > 0 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("the log was not emptied after the reader was done")
		}
	}
	stop()
}

// Closing a store while the checkpointer waits for the writer's turn
// ends: the checkpointer gets the turn, sees the store closed and stops.
// A Close that kept the turn while it waited for the checkpointer would
// hang here with its writers, and the test would end by its time limit.
func TestClosingWhileTheLogIsEmptied(t *testing.T) {
	for i := 0; i < 20; i++ {
		s, _, _, stop := writing(t, 64<<10, 4)
		time.Sleep(20 * time.Millisecond)
		closed := make(chan error, 1)
		go func() { closed <- s.Close() }()
		select {
		case err := <-closed:
			if err != nil {
				t.Fatalf("close: %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("the store did not close while its log was being emptied")
		}
		stop()
	}
}
