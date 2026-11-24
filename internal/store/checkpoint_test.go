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

// A burst of commits costs the writer nothing: the log grows past its
// limit while it is copied in the background. When the writer pauses,
// the log is emptied, with no further commit (ADR-0035, item 7).
func TestTheLogIsEmptiedAfterABurst(t *testing.T) {
	const limit = 256 << 10
	s, path := openStore(t, Options{WALLimit: limit, CheckpointEvery: 5 * time.Millisecond})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(logNote), asSetup); err != nil {
		t.Fatal(err)
	}
	// A burst: past the limit, and well short of where the writers
	// would be made to wait.
	for logSize(path) < 3*limit {
		if _, err := s.Create(ctx, "Note", Values{"text": "a note of some length, as a caller writes one"}, asSetup); err != nil {
			t.Fatal(err)
		}
	}
	if size := logSize(path); size >= farPast*limit {
		t.Fatalf("the burst of the test made a log of %d KiB, which is no burst", size>>10)
	}
	// The writer pauses, and the log is emptied.
	for until := time.Now().Add(10 * time.Second); logSize(path) > limit; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(until) {
			t.Fatalf("the log is %d KiB after the writer paused, over its limit of %d KiB", logSize(path)>>10, limit>>10)
		}
	}
}

// A writer that never pauses leaves no moment at which the log could be
// begun again, and the log of such a writer grew to gigabytes. Once the
// log is far past its limit the checkpointer takes the writer's turn
// among the commits and empties it, again and again while the writers
// write, so the log stays under a bound: eight times its limit and what
// the writers write in a moment more (ADR-0035, item 7).
//
// How much that moment holds is the machine's: how fast its writers
// commit and how fast its disk copies the log. Four writers without the
// race detector, on a disk, take a log of this test's megabyte to 20 to
// 110 MiB before the turn has emptied it, and on the Windows runner to
// 37; so the test asks for no size. Nor is a log that is emptied enough:
// without the turn the same writers leave the log, now and then, a
// moment in which it is emptied by chance, five times in two seconds,
// at 450 to 820 MiB. What only the turn does is counted: the writer's
// turn is taken and the log emptied in it, again and again.
func TestTheLogIsEmptiedInTheWritersTurn(t *testing.T) {
	const limit = 1 << 20
	// often is how many turns the test waits for: a few, which a slow
	// machine takes in seconds.
	const often = 5
	s, path, _, stop := writing(t, limit, 4)
	began := time.Now()
	var times int
	var largest int64
	for until := began.Add(30 * time.Second); s.turns.Load() < often && time.Now().Before(until); {
		more, larger := emptied(path, 250*time.Millisecond)
		times, largest = times+more, max(largest, larger)
	}
	turns, took := s.turns.Load(), time.Since(began).Round(time.Millisecond)
	stop()
	t.Logf("in %s the log was emptied %d times in the writers' turn and %d times in all, and was %d KiB at its largest, under a limit of %d KiB",
		took, turns, times, largest>>10, limit>>10)
	if turns < often {
		t.Errorf("in %s of writing without a pause the log was emptied %d times in the writers' turn, and grew to %d MiB under a limit of %d MiB",
			took, turns, largest>>20, limit>>20)
	}
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
	// While the reader keeps its view, the writers commit. A
	// checkpointer that waited for the reader in the writer's turn
	// would hold every commit for the five seconds a connection waits,
	// again and again, and let some twenty through between two waits.
	// A slow machine with the race detector makes a few hundred commits
	// in half a second. So the writers are given five seconds for a
	// hundred: a machine that stands still for a moment has them, and
	// a checkpointer that waits does not. (The test counted the commits
	// of one half second, and a runner on Windows once made none in
	// it.)
	time.Sleep(100 * time.Millisecond)
	const want, within = 100, 5 * time.Second
	before, began := commits.Load(), time.Now()
	for commits.Load()-before < want && time.Since(began) < within {
		time.Sleep(10 * time.Millisecond)
	}
	if made := commits.Load() - before; made < want {
		t.Errorf("%d commits in %s while a reader kept its view: the checkpointer held the writers for the reader", made, time.Since(began).Round(time.Millisecond))
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
