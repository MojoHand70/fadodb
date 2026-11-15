package command

import (
	"bytes"
	"encoding/json"
	"sync"
)

// Marshal writes a value as JSON for an answer: as encoding/json does,
// but with <, > and & written as themselves. encoding/json escapes the
// three so that JSON can be pasted into a page of HTML; an answer is
// not a page, a door says so in its headers, and a formula read at a
// terminal or by an agent should read "total > 0" and not
// "total > 0" (ADR-0055, amended 2026-11-15). Every answer of a
// command, of a door and of the tool is written through it or through
// the encoder of a document, which writes texts the same way.
func Marshal(v any) ([]byte, error) {
	e := encoders.Get().(*encoder)
	e.buf.Reset()
	err := e.enc.Encode(v)
	var out []byte
	if err == nil {
		// Encode ends its value with a line end.
		text := e.buf.Bytes()
		out = make([]byte, len(text)-1)
		copy(out, text)
	}
	if e.buf.Cap() <= largestKept {
		encoders.Put(e)
	}
	return out, err
}

// encoder is an encoder of encoding/json and the buffer it writes to.
type encoder struct {
	buf bytes.Buffer
	enc *json.Encoder
}

// largestKept is the largest buffer the pool keeps: one answer of a
// great many rows does not hold its memory for good.
const largestKept = 64 << 10

var encoders = sync.Pool{New: func() any {
	e := &encoder{}
	e.enc = json.NewEncoder(&e.buf)
	e.enc.SetEscapeHTML(false)
	return e
}}
