package store_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/store"
)

// streamInvoice is the class of the campaign of the stream: an invoice
// that finance reads, so that the read rule is asked for every event
// and every subscriber.
const streamInvoice = `{"name": "Invoice", "variables": {"net": {"type": "number"}},
 "states": {"initial": "review", "list": ["review"]},
 "access": {"read": "INGROUP(\"finance\")", "create": "TRUE"}}`

// TestStreamCampaign measures the stream of changes on the wire
// (ADR-0058, Performance): the time from the end of a commit to the
// moment a subscriber has read its entry from a server on this machine,
// with one subscriber and with a hundred, each of whom the read rule
// lets hear of every event. It measures a writer that commits every
// millisecond, so that each entry travels alone, and one that never
// pauses.
//
// It runs only with FADODB_BENCH_DIR set to a directory on a real disk;
// FADODB_BENCH_COMMITS sizes it, 5,000 commits for each row by default.
// It prints a Markdown table with -v.
func TestStreamCampaign(t *testing.T) {
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		t.Skip("set FADODB_BENCH_DIR to a directory on disk to run the campaign of the stream")
	}
	n := 5000
	if v, err := strconv.Atoi(os.Getenv("FADODB_BENCH_COMMITS")); err == nil && v > 0 {
		n = v
	}
	var rows []string
	for _, c := range []struct {
		subscribers int
		pause       time.Duration
	}{{1, time.Millisecond}, {100, time.Millisecond}, {1, 0}, {100, 0}} {
		rows = append(rows, streamRow(t, dir, n, c.subscribers, c.pause))
	}
	t.Logf("\n| Subscribers | The writer | Commits | Commits/s | p50 | p99 | Longest |\n|---|---|---|---|---|---|---|\n%s", strings.Join(rows, "\n"))
}

// streamRow runs one row of the campaign and answers it.
func streamRow(t *testing.T, dir string, n, subscribers int, pause time.Duration) string {
	path := filepath.Join(dir, fmt.Sprintf("stream-%d.fado", time.Now().UnixNano()))
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(path + suffix)
		}
	}()
	s, err := store.Open(path, store.Options{Outbox: store.OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	designer := store.As(store.Principal{ID: "setup", System: []string{store.GroupDesigners}})
	if _, err := s.DefineClass(ctx, []byte(streamInvoice), designer); err != nil {
		t.Fatal(err)
	}
	srv := serve(s)
	defer srv.Close()
	defer srv.CloseClientConnections()

	// ended is when the commit of each invoice ended, and got when each
	// subscriber had read its entry, both in nanoseconds since start.
	start := time.Now()
	ended := make([]atomic.Int64, n)
	got := make([][]int64, subscribers)
	var reading sync.WaitGroup
	who, _ := json.Marshal(store.Principal{ID: "ap-agent", Kind: store.ActorAgent, Groups: []string{"finance"}})
	for i := range got {
		got[i] = make([]int64, n)
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/subscribe", strings.NewReader(`{"classes": ["Invoice"], "kinds": ["created"]}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(sentAs, base64.StdEncoding.EncodeToString(who))
		// A connection of its own for each subscriber, as callers have.
		resp, err := (&http.Transport{}).RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("subscriber %d was answered with %d", i, resp.StatusCode)
		}
		in := bufio.NewReaderSize(resp.Body, 64<<10)
		// The first line says the stream is open.
		for line := ""; line != "\n"; {
			if line, err = in.ReadString('\n'); err != nil {
				t.Fatal(err)
			}
		}
		reading.Add(1)
		go func(times []int64) {
			defer reading.Done()
			defer resp.Body.Close()
			for heard := 0; heard < n; {
				line, err := in.ReadString('\n')
				if err != nil {
					t.Errorf("a subscriber read %d of %d entries: %v", heard, n, err)
					return
				}
				data, found := strings.CutPrefix(line, "data: ")
				if !found {
					continue
				}
				now := int64(time.Since(start))
				var ev struct {
					DocID string `json:"doc_id"`
				}
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					t.Errorf("an entry is not an event: %s", line)
					return
				}
				if i, err := strconv.Atoi(ev.DocID); err == nil && i >= 0 && i < n {
					times[i] = now
				}
				heard++
			}
		}(got[i])
	}

	writer := store.As(store.Principal{ID: "bob", Kind: store.ActorPerson})
	began := time.Now()
	for i := 0; i < n; i++ {
		if _, err := s.Create(ctx, "Invoice", store.Values{"net": i}, writer, store.WithID(strconv.Itoa(i))); err != nil {
			t.Fatal(err)
		}
		ended[i].Store(int64(time.Since(start)))
		if pause > 0 {
			time.Sleep(pause)
		}
	}
	wrote := time.Since(began)
	reading.Wait()

	// The time of every entry for every subscriber. An entry that was
	// read before the commit had returned to the writer took no time.
	took := make([]int64, 0, n*subscribers)
	for _, times := range got {
		for i, at := range times {
			took = append(took, max(at-ended[i].Load(), 0))
		}
	}
	sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
	at := func(q float64) time.Duration {
		return time.Duration(took[min(int(q*float64(len(took))), len(took)-1)]).Round(time.Microsecond)
	}
	how := "never pauses"
	if pause > 0 {
		how = "commits every " + pause.String()
	}
	return fmt.Sprintf("| %d | %s | %d | %.0f | %v | %v | %v |", subscribers, how, n, float64(n)/wrote.Seconds(), at(0.50), at(0.99), at(1))
}
