package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/store"
)

// cabinet stands in for a database that keeps files: one file, and what
// it was asked.
type cabinet struct {
	engine
	info  store.FileInfo
	bytes string
	// fail, when set, is what every call answers.
	fail error
	// opened counts the files and blobs that were opened and not closed.
	opened int
}

type closing struct {
	io.Reader
	c *cabinet
}

func (f closing) Close() error { f.c.opened--; return nil }

func (c *cabinet) PutBlob(_ context.Context, name, mediaType string, r io.Reader, _ ...store.Option) (store.Values, error) {
	if c.fail != nil {
		return nil, c.fail
	}
	text, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	c.bytes = string(text)
	return store.Values{"hash": c.info.Hash, "name": name, "media_type": mediaType, "size": int64(len(text))}, nil
}

func (c *cabinet) OpenFile(context.Context, string, string, string, ...store.Option) (io.ReadCloser, *store.FileInfo, error) {
	if c.fail != nil {
		return nil, nil, c.fail
	}
	c.opened++
	info := c.info
	return closing{strings.NewReader(c.bytes), c}, &info, nil
}

func (c *cabinet) OpenBlob(context.Context, string, ...store.Option) (io.ReadCloser, int64, error) {
	if c.fail != nil {
		return nil, 0, c.fail
	}
	c.opened++
	return closing{strings.NewReader(c.bytes), c}, int64(len(c.bytes)), nil
}

func get(d http.Handler, address string, header ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, address, nil)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	return w
}

const sum = "d6940f282d8cc36b76fecd3fed829379f9ef49bb2c14af90e97e9bd84761a8d7"

// A file is sent so that a browser saves it and does not show it,
// whatever its uploader said it is, and its name survives any alphabet.
func TestHowAFileIsSent(t *testing.T) {
	c := &cabinet{engine: echo, bytes: "<script>alert(1)</script>",
		info: store.FileInfo{Hash: sum, Name: "zażółć gęślą.html", MediaType: "text/html", Size: 25}}
	d := door(t, c, Options{})
	w := get(d, "/v1/file?class=Receipt&id=R-7&hash="+sum)
	if w.Code != http.StatusOK || w.Body.String() != c.bytes {
		t.Fatalf("%d %q", w.Code, w.Body)
	}
	h := w.Header()
	if got := h.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment; filename*=utf-8''za%C5%BC") {
		t.Errorf("Content-Disposition is %q", got)
	}
	if h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("a file that says it is HTML is sent with %v", h)
	}
	if h.Get("Content-Type") != "text/html" || h.Get("Content-Length") != "25" || h.Get("ETag") != `"`+sum+`"` {
		t.Errorf("the headers are %v", h)
	}

	// A file with no name and no kind.
	c.info = store.FileInfo{Hash: sum, Size: 25}
	w = get(d, "/v1/file?class=Receipt&id=R-7&hash="+sum)
	if got := w.Header().Get("Content-Disposition"); got != "attachment" {
		t.Errorf("Content-Disposition of a file without a name is %q", got)
	}
	if got := w.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type of a file without a kind is %q", got)
	}

	// The tag of the bytes, however a caller writes that it holds them.
	for _, holds := range []string{`"` + sum + `"`, `W/"` + sum + `"`, `"other", "` + sum + `"`, "*"} {
		if w := get(d, "/v1/file?class=Receipt&id=R-7&hash="+sum, "If-None-Match", holds); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
			t.Errorf("If-None-Match %s is answered %d %q", holds, w.Code, w.Body)
		}
	}
	if w := get(d, "/v1/file?class=Receipt&id=R-7&hash="+sum, "If-None-Match", `"other"`); w.Code != http.StatusOK {
		t.Errorf("a caller that holds other bytes is answered %d", w.Code)
	}
	if c.opened != 0 {
		t.Errorf("%d files were opened and not closed", c.opened)
	}
}

// The files take the slots of commands, are reported as requests, and a
// failure keeps its words.
func TestFilesAreRequestsLikeAnyOther(t *testing.T) {
	var told []Request
	c := &cabinet{engine: echo, bytes: "%PDF", info: store.FileInfo{Hash: sum, Name: "taxi.pdf", MediaType: "application/pdf", Size: 4}}
	d := door(t, c, Options{Report: func(r Request) { told = append(told, r) }, Limits: Limits{Commands: 1}})

	put := httptest.NewRequest(http.MethodPost, "/v1/blobs?name=taxi.pdf", strings.NewReader("%PDF"))
	put.Header.Set("Content-Type", "application/pdf")
	w := httptest.NewRecorder()
	d.ServeHTTP(w, put)
	if want := `{"file":{"hash":"` + sum + `","name":"taxi.pdf","media_type":"application/pdf","size":4}}` + "\n"; w.Code != 200 || w.Body.String() != want {
		t.Errorf("the put is answered %d %s", w.Code, w.Body)
	}
	get(d, "/v1/file?class=Receipt&id=R-7&hash="+sum)
	get(d, "/v1/blob?hash="+sum)
	hidden := &store.Error{Code: store.CodeUnknownDocument, Message: "no document R-8 of class Receipt at that version"}
	c.fail = hidden
	if w := get(d, "/v1/file?class=Receipt&id=R-8&hash="+sum); w.Code != 404 || refusal(t, w) != *hidden {
		t.Errorf("a refusal of the engine is answered %d %s", w.Code, w.Body)
	}
	c.fail = errors.New("disk /var/lib/acme is on fire")
	if w := get(d, "/v1/file?class=Receipt&id=R-7&hash="+sum); w.Code != 500 || strings.Contains(w.Body.String(), "on fire") {
		t.Errorf("a failure is answered %d %s", w.Code, w.Body)
	}

	want := []Request{
		{Command: "blobs", User: "ann", Status: 200},
		{Command: "file", User: "ann", Status: 200},
		{Command: "blob", User: "ann", Status: 200},
		{Command: "file", User: "ann", Status: 404, Code: store.CodeUnknownDocument},
		{Command: "file", User: "ann", Status: 500, Code: command.CodeInternal},
	}
	if len(told) != len(want) {
		t.Fatalf("%d requests were reported, want %d", len(told), len(want))
	}
	for i, got := range told {
		if i == 4 && (got.Err == nil || !strings.Contains(got.Err.Error(), "on fire")) {
			t.Errorf("the failure is reported without its words: %+v", got)
		}
		got.At, got.Took, got.Err = want[i].At, 0, nil
		if got != want[i] {
			t.Errorf("request %d is reported as %+v, want %+v", i, got, want[i])
		}
	}
	if len(d.slots) != 0 {
		t.Errorf("%d slots are held after every request ended", len(d.slots))
	}

	// A door that is full takes no file either.
	d.wait = 0
	d.slots <- struct{}{}
	c.fail = nil
	if w := get(d, "/v1/file?class=Receipt&id=R-7&hash="+sum); w.Code != http.StatusServiceUnavailable || refusal(t, w).Code != CodeBusy {
		t.Errorf("a file asked of a full door is answered %d %s", w.Code, w.Body)
	}
	<-d.slots
}

// The three addresses of the files are not commands, and never will be:
// a command of one of these names could not be reached.
func TestNoCommandTakesAnAddressOfTheFiles(t *testing.T) {
	for _, name := range []string{addressPut, addressFile, addressBlob} {
		if _, taken := command.Lookup(name); taken {
			t.Errorf("a command is named %s, the address of the files", name)
		}
	}
}
