package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/fado/httpdoor"
)

// metrics are the counts the server keeps for whoever watches it
// (ADR-0059, item 9): how many requests it answered, to which command
// and with which status, and how long they took. It answers them at
// GET /v1/metrics, with what the queue of the database still has to do,
// in the text form Prometheus reads, to a database administrator.
//
// A request is counted where the door reports it, once it was answered,
// and counting allocates nothing: a place for every command is made
// when the server starts, and a name that is no command is counted as
// one, other, so that a caller cannot make the page grow.
type metrics struct {
	version string
	started time.Time
	// place is where a name is counted; names are the names by place,
	// the last of them other.
	place map[string]int
	names []string
	// answered counts the requests by name and status, took adds up
	// their time by name, in nanoseconds, and within counts all of them
	// by how long they took.
	answered [][]atomic.Uint64
	took     []atomic.Uint64
	within   [len(withinSeconds) + 1]atomic.Uint64
	all      atomic.Uint64
}

// The address of the counts, and the name its requests are counted and
// logged under.
const (
	metricsPath = "/v1/metrics"
	metricsName = "metrics"
	otherName   = "other"
)

// statuses are the statuses a reply is counted under; any other is
// counted last, as other.
var statuses = [...]int{200, 304, 400, 401, 403, 404, 405, 409, 413, 415, 422, 500, 503, 504}

// withinSeconds are the bounds requests are counted within, in seconds.
var withinSeconds = [...]float64{0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// newMetrics makes the counts of a server: a place for every command,
// for the three addresses of the files, for the counts themselves, and
// one for every name that is none of them.
func newMetrics(version string, started time.Time) *metrics {
	m := &metrics{version: version, started: started, place: map[string]int{}}
	add := func(name string) {
		if _, twice := m.place[name]; !twice {
			m.place[name] = len(m.names)
			m.names = append(m.names, name)
		}
	}
	for _, c := range fado.Commands() {
		add(c.Name)
	}
	for _, name := range []string{"blobs", "file", "blob", metricsName, otherName} {
		add(name)
	}
	m.answered = make([][]atomic.Uint64, len(m.names))
	for i := range m.answered {
		m.answered[i] = make([]atomic.Uint64, len(statuses)+1)
	}
	m.took = make([]atomic.Uint64, len(m.names))
	return m
}

// record counts one request, as the door reports it.
func (m *metrics) record(r httpdoor.Request) {
	i, known := m.place[r.Command]
	if !known {
		i = len(m.names) - 1
	}
	j := len(statuses)
	for k, status := range statuses {
		if status == r.Status {
			j = k
			break
		}
	}
	m.answered[i][j].Add(1)
	took := max(r.Took, 0)
	m.took[i].Add(uint64(took))
	m.all.Add(uint64(took))
	b := len(withinSeconds)
	for k, bound := range withinSeconds {
		if took.Seconds() <= bound {
			b = k
			break
		}
	}
	m.within[b].Add(1)
}

// The families of the counts, in the order they are written, each with
// its kind and what it says. The manual's chapter lists them from here.
var families = []struct{ name, kind, help string }{
	{"fadodb_build_info", "gauge", "The version of the server; always 1."},
	{"fadodb_start_time_seconds", "gauge", "When the server started, in seconds since 1970."},
	{"fadodb_requests_total", "counter", "Requests answered, by the command they were sent to and the status of the reply. A name that is no command is counted as other."},
	{"fadodb_command_seconds", "summary", "The time the requests of a command took, added up, and how many they were."},
	{"fadodb_request_seconds", "histogram", "How long requests took, all commands together."},
	{"fadodb_actions", "gauge", "The queued actions of the database that are not done: due and not yet run, waiting for their moment, and dead."},
	{"fadodb_actions_oldest_due_seconds", "gauge", "How long the action that has been due longest has been due; 0 when none is due."},
	{"fadodb_database_bytes", "gauge", "The size of the database file."},
	{"fadodb_log_bytes", "gauge", "The size of the file of latest commits beside the database, which is folded into it."},
}

// write writes the counts as they are now, with what the queue of the
// database has to do and the sizes of its files, in the text form
// Prometheus reads.
func (m *metrics) write(w io.Writer, queue fado.OutboxCounts, databaseBytes, logBytes int64, now time.Time) {
	b := make([]byte, 0, 8<<10)
	head := func(i int) {
		f := families[i]
		b = append(b, "# HELP "+f.name+" "+f.help+"\n# TYPE "+f.name+" "+f.kind+"\n"...)
	}
	line := func(name, labels string, value float64) {
		b = append(b, name...)
		if labels != "" {
			b = append(append(append(b, '{'), labels...), '}')
		}
		b = append(b, ' ')
		// Written out in full: a size or a moment with an exponent is
		// right for a program and hard on a person who reads the page.
		b = strconv.AppendFloat(b, value, 'f', -1, 64)
		b = append(b, '\n')
	}
	seconds := func(nanos uint64) float64 { return float64(nanos) / 1e9 }

	head(0)
	line("fadodb_build_info", `version=`+label(m.version), 1)
	head(1)
	line("fadodb_start_time_seconds", "", float64(m.started.UnixNano())/1e9)

	head(2)
	counts := make([]uint64, len(m.names))
	for i, name := range m.names {
		for j := range m.answered[i] {
			n := m.answered[i][j].Load()
			if n == 0 {
				continue
			}
			counts[i] += n
			status := otherName
			if j < len(statuses) {
				status = strconv.Itoa(statuses[j])
			}
			line("fadodb_requests_total", `command=`+label(name)+`,status="`+status+`"`, float64(n))
		}
	}
	head(3)
	for i, name := range m.names {
		if counts[i] == 0 {
			continue
		}
		line("fadodb_command_seconds_sum", `command=`+label(name), seconds(m.took[i].Load()))
		line("fadodb_command_seconds_count", `command=`+label(name), float64(counts[i]))
	}
	head(4)
	var upTo uint64
	for k, bound := range withinSeconds {
		upTo += m.within[k].Load()
		line("fadodb_request_seconds_bucket", `le="`+strconv.FormatFloat(bound, 'f', -1, 64)+`"`, float64(upTo))
	}
	upTo += m.within[len(withinSeconds)].Load()
	line("fadodb_request_seconds_bucket", `le="+Inf"`, float64(upTo))
	line("fadodb_request_seconds_sum", "", seconds(m.all.Load()))
	line("fadodb_request_seconds_count", "", float64(upTo))

	head(5)
	line("fadodb_actions", `state="due"`, float64(queue.Due))
	line("fadodb_actions", `state="waiting"`, float64(queue.Waiting))
	line("fadodb_actions", `state="dead"`, float64(queue.Dead))
	head(6)
	oldest := 0.0
	if !queue.OldestDue.IsZero() && now.After(queue.OldestDue) {
		oldest = now.Sub(queue.OldestDue).Seconds()
	}
	line("fadodb_actions_oldest_due_seconds", "", oldest)
	head(7)
	line("fadodb_database_bytes", "", float64(databaseBytes))
	head(8)
	line("fadodb_log_bytes", "", float64(logBytes))
	_, _ = w.Write(b)
}

// label writes a text as the value of a label.
func label(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}

// fileBytes is the size of a file, 0 when there is none.
func fileBytes(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// counts answers the counts of the server to a database administrator
// (ADR-0059, item 9). The caller says who it is with its key, as for a
// command; the queue is counted as that caller, so the engine itself
// refuses whoever is no administrator.
func (s *served) counts(w http.ResponseWriter, r *http.Request) {
	told := httpdoor.Request{At: time.Now(), Command: metricsName}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	refuse := func(status int, e *fado.Error) {
		told.Status, told.Code = status, e.Code
		h.Set("Content-Type", "application/json")
		w.WriteHeader(status)
		text, _ := json.Marshal(struct {
			Error *fado.Error `json:"error"`
		}{e})
		_, _ = w.Write(append(text, '\n'))
	}
	defer func() {
		told.Took = time.Since(told.At)
		s.report(told)
	}()
	if r.Method != http.MethodGet {
		h.Set("Allow", http.MethodGet)
		refuse(http.StatusMethodNotAllowed, &fado.Error{Code: "bad_json", Message: "the counts of the server are read with GET"})
		return
	}
	who, err := s.who(r)
	if err != nil {
		h.Set("WWW-Authenticate", "Bearer")
		refuse(httpdoor.Status("unauthenticated"), &fado.Error{Code: "unauthenticated",
			Message: "the request does not say who sends it, or says it with a credential the server does not know"})
		return
	}
	told.User = who.ID
	queue, err := s.db.OutboxCounts(r.Context(), fado.As(who))
	if err != nil {
		var refused *fado.Error
		if !errors.As(err, &refused) || refused.Code == "storage" {
			told.Err = err
			refused = &fado.Error{Code: "internal", Message: "the counts could not be read; the program that runs the server has the words"}
		}
		refuse(httpdoor.Status(refused.Code), refused)
		return
	}
	told.Status = http.StatusOK
	h.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	s.metrics.write(w, queue, fileBytes(s.path), fileBytes(s.path+"-wal"), time.Now())
}
