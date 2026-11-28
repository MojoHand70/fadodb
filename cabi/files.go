package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"

	"github.com/MojoHand70/fadodb/fado"
)

// The files travel beside the commands, as over HTTP (ADR-0033, item 6;
// ADR-0055, item 9): their bytes are no JSON. What a file is on a
// document, its facts, is a value like any other, and a command commits
// it. Each function takes the database, the caller and one JSON object,
// as fado_exec does, and the bytes beside them.

// putFile is fado_put_file: it keeps the bytes and answers the facts to
// commit, {"file": {...}}, as POST /v1/blobs answers them.
func putFile(n uint64, caller, object string, data []byte) (answer []byte) {
	defer caught("fado_put_file", &answer)
	return onDatabase(n, caller, func(db *database, p fado.Principal) []byte {
		members, e := given(object, "the file", nil, "name", "media_type")
		if e != nil {
			return refuse(e)
		}
		fact, err := db.db.PutBlob(db.ctx, members["name"], members["media_type"], bytes.NewReader(data), fado.As(p))
		if err != nil {
			return failed(err)
		}
		var file fado.FileInfo
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
		return fileAnswer(file)
	})
}

// getFile is fado_get_file: the facts and the bytes of a file a document
// holds, under the rules of the document, as GET /v1/file streams it.
func getFile(n uint64, caller, object string) (answer, data []byte) {
	defer caught("fado_get_file", &answer)
	answer = onDatabase(n, caller, func(db *database, p fado.Principal) []byte {
		members, e := given(object, "the file", []string{"class", "id", "hash"}, "class", "id", "hash", "version", "for", "reason")
		if e != nil {
			return refuse(e)
		}
		p.For = members["for"]
		opts := []fado.Option{fado.As(p)}
		if text, ok := members["version"]; ok {
			version, err := strconv.ParseInt(text, 10, 64)
			if err != nil || version < 1 {
				return refuse(&fado.Error{Code: codeBadJSON, Path: "/version",
					Message: "version is " + text + ", and the version of a document is a whole number from 1",
					Hint:    "leave version out for the document as it is now"})
			}
			opts = append(opts, fado.AtVersion(version))
		}
		if reason := members["reason"]; reason != "" {
			opts = append(opts, fado.Reason(reason))
		}
		from, info, err := db.db.OpenFile(db.ctx, members["class"], members["id"], members["hash"], opts...)
		if err != nil {
			return failed(err)
		}
		defer from.Close()
		if data, err = io.ReadAll(from); err != nil {
			data = nil
			return failed(err)
		}
		return fileAnswer(*info)
	})
	return answer, data
}

// getBlob is fado_get_blob: the bytes of a hash alone, which the engine
// opens for a content administrator and for nobody else, with a reason
// the log of reads keeps (ADR-0052, ADR-0067), as GET /v1/blob does.
func getBlob(n uint64, caller, object string) (answer, data []byte) {
	defer caught("fado_get_blob", &answer)
	answer = onDatabase(n, caller, func(db *database, p fado.Principal) []byte {
		members, e := given(object, "the blob", []string{"hash"}, "hash", "reason")
		if e != nil {
			return refuse(e)
		}
		from, size, err := db.db.OpenBlob(db.ctx, members["hash"], fado.As(p), fado.Reason(members["reason"]))
		if err != nil {
			return failed(err)
		}
		defer from.Close()
		if data, err = io.ReadAll(from); err != nil {
			data = nil
			return failed(err)
		}
		return fileAnswer(fado.FileInfo{Hash: members["hash"], Size: size})
	})
	return answer, data
}

// given reads the object of a function of files: members of text, and
// version, a whole number, written back as text. needed are the ones
// that must be given.
func given(object, what string, needed []string, takes ...string) (map[string]string, *fado.Error) {
	members, e := readObject(object, what)
	if e != nil {
		return nil, e
	}
	out := make(map[string]string, len(members))
	for name, raw := range members {
		known := false
		for _, t := range takes {
			known = known || t == name
		}
		if !known {
			return nil, &fado.Error{Code: codeBadJSON, Path: pointer(name),
				Message: what + " has no member " + strconv.Quote(name),
				Hint:    "the members are " + list(takes)}
		}
		if name == "version" {
			var v int64
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, wrongType(name, what, err)
			}
			out[name] = strconv.FormatInt(v, 10)
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, wrongType(name, what, err)
		}
		out[name] = s
	}
	for _, name := range needed {
		if out[name] == "" {
			return nil, &fado.Error{Code: codeBadJSON, Path: pointer(name),
				Message: what + " needs " + name, Hint: "give " + list(needed)}
		}
	}
	return out, nil
}

func list(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	out := ""
	for i, n := range names {
		switch {
		case i == 0:
		case i == len(names)-1:
			out += " and "
		default:
			out += ", "
		}
		out += n
	}
	return out
}

func fileAnswer(file fado.FileInfo) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(struct {
		File fado.FileInfo `json:"file"`
	}{file})
	return bytes.TrimRight(b.Bytes(), "\n")
}
