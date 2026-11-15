package server

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/store"
)

// The files travel beside the commands (ADR-0033; ADR-0055, item 9):
// their bytes are no JSON, so they have three addresses of their own
// under /v1/. What a file is on a document, its facts, is a value like
// any other and travels in the commands.
const (
	// addressPut takes the bytes of a file and answers its facts:
	// POST /v1/blobs?name=taxi.pdf.
	addressPut = "blobs"
	// addressFile streams a file through the document that holds it:
	// GET /v1/file?class=Receipt&id=R-7&hash=d694….
	addressFile = "file"
	// addressBlob streams bytes by their hash alone, for a content
	// administrator: GET /v1/blob?hash=d694….
	addressBlob = "blob"
)

// put keeps the bytes of a file and answers the facts to commit.
func (d *Door) put(out *reply, r *http.Request, told *Request) {
	if r.Method != http.MethodPost {
		d.notSentWith(out, told, http.MethodPost, "a file is put with POST, and this request is a "+r.Method,
			"send POST /v1/blobs?name=<the name of the file> with the bytes of the file as the body")
		return
	}
	p, ok := d.sender(out, r, told)
	if !ok {
		return
	}
	asked, refusal := parameters(r, addressPut, "name")
	if refusal != nil {
		d.refused(out, told, refusal)
		return
	}
	mediaType := r.Header.Get("Content-Type")
	if form(mediaType) {
		// A form wraps the bytes; kept as they come, the file would be
		// the wrapping.
		d.refused(out, told, &store.Error{
			Code:    command.CodeBadJSON,
			Message: "the body of this request is a form, and the body of a put is the bytes of the file",
			Hint:    "send the bytes themselves as the body, with their own Content-Type, and the name in the address: POST /v1/blobs?name=taxi.pdf",
		})
		return
	}
	if !d.slot(out, r, told) {
		return
	}
	defer d.leave()

	fact, err := d.db.PutBlob(r.Context(), asked.Get("name"), mediaType, r.Body, store.As(p))
	if err != nil {
		d.refused(out, told, err)
		return
	}
	var file store.FileInfo
	file.Hash, _ = fact["hash"].(string)
	file.Name, _ = fact["name"].(string)
	file.MediaType, _ = fact["media_type"].(string)
	switch size := fact["size"].(type) {
	case int64:
		file.Size = size
	case int:
		file.Size = int64(size)
	case float64:
		file.Size = int64(size)
	}
	text, err := command.Marshal(struct {
		File store.FileInfo `json:"file"`
	}{file})
	if err != nil {
		d.refused(out, told, err)
		return
	}
	out.send(http.StatusOK, text)
}

// file streams a file a document holds, under the rules of the
// document.
func (d *Door) file(out *reply, r *http.Request, told *Request) {
	if r.Method != http.MethodGet {
		d.notSentWith(out, told, http.MethodGet, "a file is read with GET, and this request is a "+r.Method,
			"send GET /v1/file?class=<class>&id=<id of the document>&hash=<hash of the file>")
		return
	}
	p, ok := d.sender(out, r, told)
	if !ok {
		return
	}
	asked, refusal := parameters(r, addressFile, "class", "id", "hash", "version", "for")
	if refusal == nil {
		refusal = needs(asked, addressFile, "class", "id", "hash")
	}
	if refusal != nil {
		d.refused(out, told, refusal)
		return
	}
	opts := make([]store.Option, 0, 2)
	if text := asked.Get("version"); text != "" {
		version, err := strconv.ParseInt(text, 10, 64)
		if err != nil || version < 1 {
			d.refused(out, told, &store.Error{
				Code:    command.CodeBadJSON,
				Message: "version is " + strconv.Quote(text) + ", and the version of a document is a whole number from 1",
				Path:    "/version",
			})
			return
		}
		opts = append(opts, store.AtVersion(version))
	}
	// For whom the caller acts is said with the request, as in a
	// command, and the engine checks it.
	p.For = asked.Get("for")
	opts = append(opts, store.As(p))
	if !d.slot(out, r, told) {
		return
	}
	defer d.leave()

	from, info, err := d.db.OpenFile(r.Context(), asked.Get("class"), asked.Get("id"), asked.Get("hash"), opts...)
	if err != nil {
		d.refused(out, told, err)
		return
	}
	defer from.Close()
	out.stream(r, from, info.Hash, info.Name, info.MediaType, info.Size)
}

// blob streams bytes by their hash alone. The engine opens them for a
// content administrator and for nobody else (ADR-0052).
func (d *Door) blob(out *reply, r *http.Request, told *Request) {
	if r.Method != http.MethodGet {
		d.notSentWith(out, told, http.MethodGet, "bytes are read with GET, and this request is a "+r.Method,
			"send GET /v1/blob?hash=<hash of the bytes>")
		return
	}
	p, ok := d.sender(out, r, told)
	if !ok {
		return
	}
	asked, refusal := parameters(r, addressBlob, "hash")
	if refusal == nil {
		refusal = needs(asked, addressBlob, "hash")
	}
	if refusal != nil {
		d.refused(out, told, refusal)
		return
	}
	if !d.slot(out, r, told) {
		return
	}
	defer d.leave()

	hash := asked.Get("hash")
	from, size, err := d.db.OpenBlob(r.Context(), hash, store.As(p))
	if err != nil {
		d.refused(out, told, err)
		return
	}
	defer from.Close()
	out.stream(r, from, hash, "", "", size)
}

// refused answers a request beside the commands that the engine, or
// the door, does not carry out.
func (d *Door) refused(out *reply, told *Request, err error) {
	var refusal *store.Error
	if errors.As(err, &refusal) && refusal.Code != command.CodeInternal {
		told.Code = refusal.Code
		out.refusal(Status(refusal.Code), refusal)
		return
	}
	told.Code = command.CodeInternal
	told.Err = err
	out.refusal(Status(command.CodeInternal), failed())
}

// parameters reads the parameters of an address that takes some. As
// with the members of a command, one the address does not take is
// refused and not passed over, and so is one given twice.
func parameters(r *http.Request, address string, takes ...string) (url.Values, *store.Error) {
	asked, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, &store.Error{
			Code:    command.CodeBadJSON,
			Message: "the parameters of the address cannot be read: " + err.Error(),
			Hint:    "write each as name=value, with the value escaped, such as id=FV%2F2026%2F11%2F0042",
		}
	}
	for name, values := range asked {
		known := false
		for _, take := range takes {
			known = known || name == take
		}
		if !known {
			return nil, &store.Error{
				Code:    command.CodeBadJSON,
				Message: address + " takes no " + strconv.Quote(name),
				Hint:    address + " takes " + strings.Join(takes, ", "),
				Path:    "/" + name,
			}
		}
		if len(values) > 1 {
			return nil, &store.Error{
				Code:    command.CodeBadJSON,
				Message: name + " is given " + strconv.Itoa(len(values)) + " times",
				Path:    "/" + name,
			}
		}
	}
	return asked, nil
}

// needs refuses a request that lacks a parameter its address needs.
func needs(asked url.Values, address string, names ...string) *store.Error {
	for _, name := range names {
		if asked.Get(name) == "" {
			return &store.Error{
				Code:    command.CodeBadJSON,
				Message: address + " needs " + name,
				Hint:    "such as GET /v1/" + address + "?" + strings.Join(names, "=…&") + "=…",
				Path:    "/" + name,
			}
		}
	}
	return nil
}

// form reports whether a Content-Type is one of the two a browser's
// form sends.
func form(contentType string) bool {
	kind, _, _ := mime.ParseMediaType(contentType)
	return kind == "multipart/form-data" || kind == "application/x-www-form-urlencoded"
}

// stream writes bytes as the body of a reply. The hash is their tag: a
// caller that holds them already, and says so, is answered that they
// have not changed, after the engine has opened them for it once more.
// The reply tells a browser to save the bytes and not to show them,
// whatever kind their uploader said they are.
func (o *reply) stream(r *http.Request, from io.Reader, hash, name, mediaType string, size int64) {
	h := o.w.Header()
	tag := `"` + hash + `"`
	h.Set("ETag", tag)
	h.Set("Cache-Control", "private, no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	if held(r.Header.Get("If-None-Match"), tag) {
		o.status, o.wrote = http.StatusNotModified, true
		o.w.WriteHeader(http.StatusNotModified)
		return
	}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	h.Set("Content-Type", mediaType)
	h.Set("Content-Length", strconv.FormatInt(size, 10))
	disposition := "attachment"
	if name != "" {
		if named := mime.FormatMediaType("attachment", map[string]string{"filename": name}); named != "" {
			disposition = named
		}
	}
	h.Set("Content-Disposition", disposition)
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	o.status, o.wrote = http.StatusOK, true
	o.w.WriteHeader(http.StatusOK)
	// A caller that left is no failure of the door.
	_, _ = io.Copy(o.w, from)
}

// held reports whether the tags a caller says it holds, in
// If-None-Match, name the tag.
func held(header, tag string) bool {
	for _, one := range strings.Split(header, ",") {
		one = strings.TrimPrefix(strings.TrimSpace(one), "W/")
		if one == tag || one == "*" {
			return true
		}
	}
	return false
}
