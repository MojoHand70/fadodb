package store_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/server"
	"github.com/MojoHand70/fadodb/internal/store"
)

// The command set and the HTTP door import the store, so the store's
// own tests cannot import them. This file is outside the package and in
// its test binary: it hands both to the suites, which then run through
// them as they run through the Go API (ADR-0055, item 11).
func init() {
	store.SetExecCommand(command.Exec)
	store.SetListenHTTP(listen)
	store.SetHandleHTTP(func(s *store.Store, as store.Principal) http.Handler {
		door, err := server.New(database{s}, server.Options{
			Who: func(*http.Request) (store.Principal, error) { return as, nil },
		})
		if err != nil {
			panic(err)
		}
		return door
	})
}

// database is a store as the door sees a database.
type database struct{ s *store.Store }

func (d database) Exec(ctx context.Context, p store.Principal, name string, object []byte) ([]byte, error) {
	return command.Exec(ctx, d.s, p, name, object)
}

func (d database) PutBlob(ctx context.Context, name, mediaType string, r io.Reader, opts ...store.Option) (store.Values, error) {
	return d.s.PutBlob(ctx, name, mediaType, r, opts...)
}

func (d database) OpenFile(ctx context.Context, class, id, hash string, opts ...store.Option) (io.ReadCloser, *store.FileInfo, error) {
	return d.s.OpenFile(ctx, class, id, hash, opts...)
}

func (d database) OpenBlob(ctx context.Context, hash string, opts ...store.Option) (io.ReadCloser, int64, error) {
	return d.s.OpenBlob(ctx, hash, opts...)
}

// sentAs is the header in which a step of a suite says who sends it.
// The door is given a function that believes it, which no door outside
// a test is.
const sentAs = "X-Sent-As"

// listen opens the HTTP door of a store on this machine and answers how
// a command is sent to it.
func listen(s *store.Store) (func(ctx context.Context, p store.Principal, name string, object []byte) ([]byte, error), func()) {
	door, err := server.New(database{s}, server.Options{
		Who: func(r *http.Request) (store.Principal, error) {
			var p store.Principal
			text, err := base64.StdEncoding.DecodeString(r.Header.Get(sentAs))
			if err != nil {
				return p, err
			}
			return p, json.Unmarshal(text, &p)
		},
	})
	if err != nil {
		panic(err)
	}
	srv := httptest.NewServer(door)
	client := srv.Client()
	send := func(ctx context.Context, p store.Principal, name string, object []byte) ([]byte, error) {
		who, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/"+name, bytes.NewReader(object))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(sentAs, base64.StdEncoding.EncodeToString(who))
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		if got := resp.Header.Get("Content-Type"); got != "application/json" {
			return nil, fmt.Errorf("the reply to %s says its body is %q", name, got)
		}
		if !bytes.HasSuffix(body, []byte("\n")) {
			return nil, fmt.Errorf("the reply to %s does not end with a line break: %s", name, body)
		}
		answer := bytes.TrimSuffix(body, []byte("\n"))
		// The refusal a body carries is the error, as the command set
		// returns it beside the answer.
		var got struct {
			Error       *store.Error      `json:"error"`
			Diagnostics model.Diagnostics `json:"diagnostics"`
		}
		if err := json.Unmarshal(answer, &got); err != nil {
			return nil, fmt.Errorf("the reply to %s is not a JSON object: %s", name, body)
		}
		code := ""
		if got.Error != nil {
			code = got.Error.Code
		}
		if want := server.Status(code); resp.StatusCode != want {
			return nil, fmt.Errorf("the reply to %s has the status %d and the code %q, whose status is %d", name, resp.StatusCode, code, want)
		}
		switch {
		case got.Error == nil:
			return answer, nil
		case code == command.CodeBadDefinition:
			return answer, got.Diagnostics
		case code == command.CodeInternal:
			return answer, errors.New(strings.TrimSpace(string(answer)))
		}
		return answer, got.Error
	}
	return send, srv.Close
}
