package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// BenchmarkRecalculate is the compute step of a commit on the invoice
// class: every auto variable and the title, from the dirty flags all set,
// as a create runs it. It is the hot path of ADR-0012 inside the store and
// is what the benchmark gate watches; the write step is SQLite's and is
// measured by BenchmarkCommit.
func BenchmarkRecalculate(b *testing.B) {
	dir := b.TempDir()
	s, err := Open(filepath.Join(dir, "bench.fado"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(invoice)); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Invoice", sampleValues(), As(alice))
	if err != nil {
		b.Fatal(err)
	}
	c, _ := s.class("Invoice")
	row, err := c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, doc.ID), false)
	if err != nil {
		b.Fatal(err)
	}
	evalCtx := &formula.Context{User: "alice", Now: time.Now().Unix()}
	var errs []*Error
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		dirty := s.dirtyFor(c)
		if err := s.recalc(c, row, dirty, true, evalCtx, false, &errs); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCommit is one commit end to end, compute and write: a set of
// one input on the invoice class, with its recalculation, history row,
// child rows carried forward and event, one transaction per commit. It
// writes to disk and runs only when FADODB_BENCH_DIR names a directory
// on a real disk, since a temporary directory may be in memory
// (docs/perf/README.md).
func BenchmarkCommit(b *testing.B) {
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		b.Skip("set FADODB_BENCH_DIR to a directory on disk to run the commit benchmark")
	}
	path := filepath.Join(dir, fmt.Sprintf("commit-%d.fado", time.Now().UnixNano()))
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(path + suffix)
		}
	}()
	s, err := Open(path, Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(invoice)); err != nil {
		b.Fatal(err)
	}
	doc, err := s.Create(ctx, "Invoice", sampleValues(), As(alice))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	i := 0
	for b.Loop() {
		i++
		if _, err := s.Set(ctx, "Invoice", doc.ID, Values{"discount": fmt.Sprintf("0.%02d", i%50)}, As(alice)); err != nil {
			b.Fatal(err)
		}
	}
}
