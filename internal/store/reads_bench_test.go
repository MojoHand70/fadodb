package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The benchmarks of the log of reads (ADR-0067). Each reads one salary
// review of the chapter's case on disk, since a read past a rule now
// writes: by Ewa, whom the rules let through and who costs no entry;
// by Olga, a content administrator who gives a reason, whose read is
// written down before it answers; and Marek's SQL statement, which is
// always written down. The three use nothing the engine had not before
// the log, so that the same file run on the commit before it measures
// the same reads with no entry. They write to disk and run only with
// FADODB_BENCH_DIR, like BenchmarkCommit.

func benchAcme(b *testing.B) (*Store, string) {
	b.Helper()
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		b.Skip("set FADODB_BENCH_DIR to a directory on disk to run the benchmarks of the log of reads")
	}
	path := filepath.Join(dir, fmt.Sprintf("reads-%d.fado", time.Now().UnixNano()))
	b.Cleanup(func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(path + suffix)
		}
	})
	s, err := Open(path, Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(salaryReview), asSetup); err != nil {
		b.Fatal(err)
	}
	d, err := s.Create(ctx, "SalaryReview", Values{"employee": "jan", "manager": "ewa", "salary": 21000}, As(acmeHana))
	if err != nil {
		b.Fatal(err)
	}
	return s, d.ID
}

func benchRead(b *testing.B, opts ...Option) {
	s, id := benchAcme(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := s.Get(ctx, "SalaryReview", id, opts...); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkReadByRule is Ewa's read of the review she manages.
func BenchmarkReadByRule(b *testing.B) { benchRead(b, As(acmeEwa)) }

// BenchmarkReadPastRule is Olga's read of the same review past its rule,
// with a reason.
func BenchmarkReadPastRule(b *testing.B) { benchRead(b, As(acmeOlga), why) }

// BenchmarkSQLStatement is one statement of Marek's with an argument.
func BenchmarkSQLStatement(b *testing.B) {
	s, _ := benchAcme(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := s.SQL(ctx, `SELECT employee, salary FROM SalaryReview WHERE salary > ?`, []any{20000}, As(acmeMarek)); err != nil {
			b.Fatal(err)
		}
	}
}
