package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/store"
)

// addressBundle is the bundle's form for a browser's cache (ADR-0070,
// item 2; ADR-0055, item 8): GET /v1/bundle?class=Invoice&language=pl
// answers what the command get_bundle answers, with its tag as the ETag.
const addressBundle = "bundle"

// bundle answers the bundle of a class to a GET, so that a browser keeps
// it and asks each time whether it changed. It is the command get_bundle,
// sent by the one piece of Go every door uses (ADR-0055, item 10).
func (d *Door) bundle(out *reply, r *http.Request, told *Request) {
	if r.Method != http.MethodGet {
		d.notSentWith(out, told, http.MethodGet, "the bundle is read with GET, and this request is a "+r.Method,
			"send GET /v1/bundle?class=<class>&language=<language>, or the command POST /v1/get_bundle")
		return
	}
	p, ok := d.sender(out, r, told)
	if !ok {
		return
	}
	asked, refusal := parameters(r, addressBundle, "class", "language", "version")
	if refusal == nil {
		refusal = needs(asked, addressBundle, "class")
	}
	if refusal != nil {
		d.refused(out, told, refusal)
		return
	}
	object := map[string]any{"class": asked.Get("class")}
	if language := asked.Get("language"); language != "" {
		object["language"] = language
	}
	if text := asked.Get("version"); text != "" {
		version, err := strconv.ParseInt(text, 10, 64)
		if err != nil || version < 1 {
			d.refused(out, told, &store.Error{
				Code:    command.CodeBadJSON,
				Message: "version is " + strconv.Quote(text) + ", and the version of a class is a whole number from 1",
				Path:    "/version",
			})
			return
		}
		object["version"] = version
	}
	if tag := heldTag(r.Header.Get("If-None-Match")); tag != "" {
		object["tag"] = tag
	}
	body, err := json.Marshal(object)
	if err != nil {
		d.refused(out, told, err)
		return
	}
	if !d.slot(out, r, told) {
		return
	}
	defer d.leave()

	ctx, cancel := context.WithTimeout(r.Context(), d.limits.CommandTime)
	answer, err := d.db.Exec(ctx, p, "get_bundle", body)
	cancel()
	if err != nil {
		code := command.Code(err)
		told.Code = code
		if code == command.CodeInternal {
			told.Err = err
		}
		out.send(Status(code), answer)
		return
	}
	var said struct {
		Unchanged bool   `json:"unchanged"`
		Tag       string `json:"tag"`
	}
	if err := json.Unmarshal(answer, &said); err != nil {
		d.refused(out, told, err)
		return
	}
	out.tagged(answer, said.Tag, said.Unchanged)
}

// heldTag is the tag a browser says it holds in If-None-Match: the
// first, without its quotes and its mark of a weak tag.
func heldTag(header string) string {
	first, _, _ := strings.Cut(header, ",")
	first = strings.TrimPrefix(strings.TrimSpace(first), "W/")
	if len(first) < 2 || first[0] != '"' || first[len(first)-1] != '"' {
		return ""
	}
	return first[1 : len(first)-1]
}

// tagged writes an answer a browser may keep: with its tag as the ETag,
// kept for the caller alone and asked after each time. A browser that
// holds it is told it has not changed, and nothing more.
func (o *reply) tagged(answer []byte, tag string, unchanged bool) {
	h := o.w.Header()
	h.Set("ETag", `"`+tag+`"`)
	h.Set("Cache-Control", "private, no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	if unchanged {
		o.status, o.wrote = http.StatusNotModified, true
		o.w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json")
	answer = append(answer, '\n')
	h.Set("Content-Length", strconv.Itoa(len(answer)))
	o.status, o.wrote = http.StatusOK, true
	o.w.WriteHeader(http.StatusOK)
	// A caller that left is no failure of the door.
	_, _ = o.w.Write(answer)
}
