// Package web holds the files the server's own pages load (ADR-0088,
// item 7): their style, their one script, the typefaces of Rhea's
// sign-in, Schibsted Grotesk and Martian Mono, under the SIL Open Font
// License (their licences are in fonts/), and FadoDB's mark. They are in
// the program, so the server loads nothing from elsewhere; they are the
// same for every database it serves and never change while it runs.
//
// Each is served under /auth/ at an address that holds the start of its
// hash, so that a browser keeps it for a year and a new program names
// new addresses.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"strings"
)

//go:embed pages.css pages.js mark.png fonts/*.woff2
var files embed.FS

// File is one file the pages load: what it holds, its kind, and the
// address it is served at.
type File struct {
	Body []byte
	Kind string
	At   string
}

// kinds are the files, by name, with their kinds. The typefaces come
// before the style, which names them.
var kinds = []struct{ name, kind string }{
	{"fonts/schibsted-grotesk-latin.woff2", "font/woff2"},
	{"fonts/schibsted-grotesk-latin-ext.woff2", "font/woff2"},
	{"fonts/martian-mono-latin.woff2", "font/woff2"},
	{"fonts/martian-mono-latin-ext.woff2", "font/woff2"},
	{"mark.png", "image/png"},
	{"pages.js", "text/javascript; charset=utf-8"},
	{"pages.css", "text/css; charset=utf-8"},
}

// served and named are the files by their addresses and by their names.
var served, named = load()

func load() (map[string]*File, map[string]*File) {
	byAt, byName := map[string]*File{}, map[string]*File{}
	for _, k := range kinds {
		body, err := files.ReadFile(k.name)
		if err != nil {
			panic("web: the program lacks " + k.name)
		}
		if k.name == "pages.css" {
			text := string(body)
			for name, f := range byName {
				text = strings.ReplaceAll(text, "{{"+name+"}}", f.At)
			}
			if strings.Contains(text, "{{") {
				panic("web: pages.css names a file the program lacks")
			}
			body = []byte(text)
		}
		sum := sha256.Sum256(body)
		dot := strings.LastIndexByte(k.name, '.')
		f := &File{Body: body, Kind: k.kind, At: "/auth/" + k.name[:dot] + "." + hex.EncodeToString(sum[:5]) + k.name[dot:]}
		byAt[f.At], byName[k.name] = f, f
	}
	return byAt, byName
}

// At is the address a page names a file by, such as "pages.js".
func At(name string) string {
	if f := named[name]; f != nil {
		return f.At
	}
	return ""
}

// Served is the file served at an address, or nil.
func Served(path string) *File { return served[path] }
