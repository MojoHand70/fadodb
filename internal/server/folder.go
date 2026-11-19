package server

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// A Folder hands out the files of a folder as the pages of a face
// (ADR-0073). The server has no pages of a face of its own: the operator
// names a folder, FadoDB Workshop's or one of their own, and its files
// answer at the root of the server's address, where the server's own
// addresses always come first.
//
// A script of such a page runs at the server's own address, so it sends
// commands with the session of whoever is signed in. The folder is
// therefore the operator's to answer for, as the program and its
// settings are, and the server sends every file with one header that is
// not the operator's to change: a page loads script, style, pictures and
// fonts from the server alone, calls the server alone, and is shown
// inside no other page.
type Folder struct {
	root   *os.Root
	report func(Request)
}

// FolderIndex is the file a folder answers its own address with, and the
// address of any folder inside it.
const FolderIndex = "index.html"

// FolderPolicy is the Content-Security-Policy every file of a folder is
// sent with. Styles may be written in a page: a text editor that a page
// loads sets its own, and a style runs nothing.
const FolderPolicy = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

// FolderKind is a kind of file a folder hands out: the ending of its
// name and the type it is sent as.
type FolderKind struct {
	Ending string
	Type   string
	What   string
}

// FolderKinds are the kinds of file a folder hands out, and no other: a
// file whose name ends otherwise is not there.
func FolderKinds() []FolderKind {
	kinds := []FolderKind{
		{Ending: ".html", What: "a page"},
		{Ending: ".css", What: "a style sheet"},
		{Ending: ".js", What: "a script"},
		{Ending: ".mjs", What: "a script"},
		{Ending: ".json", What: "data"},
		{Ending: ".txt", What: "a text, such as a licence"},
		{Ending: ".svg", What: "a drawing"},
		{Ending: ".png", What: "a picture"},
		{Ending: ".jpg", What: "a picture"},
		{Ending: ".jpeg", What: "a picture"},
		{Ending: ".gif", What: "a picture"},
		{Ending: ".webp", What: "a picture"},
		{Ending: ".ico", What: "the icon of a tab"},
		{Ending: ".woff2", What: "a font"},
	}
	for i := range kinds {
		kinds[i].Type, _ = folderKind(kinds[i].Ending)
	}
	return kinds
}

// FolderReserved are the first parts of the server's own addresses. A
// file of a folder under one of these names is never handed out.
func FolderReserved() []string { return []string{"v1", "auth", "mcp", ".well-known"} }

// NewFolder opens the folder at dir. It refuses a folder that is not
// there or has no index.html, so that a mistake is found when the server
// starts and not by the first person who opens it. report, when given,
// is told of each request once it was answered.
func NewFolder(dir string, report func(Request)) (*Folder, error) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, errors.New("there is no folder of pages at " + dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.New("the folder of pages at " + dir + " cannot be opened: " + err.Error())
	}
	if first, err := root.Stat(FolderIndex); err != nil || first.IsDir() {
		_ = root.Close()
		return nil, errors.New("the folder of pages at " + dir + " has no " + FolderIndex + ", the page its address answers with")
	}
	return &Folder{root: root, report: report}, nil
}

// Close lets go of the folder.
func (f *Folder) Close() error { return f.root.Close() }

// Shadowed lists the names in the folder that are the server's own
// addresses, and so are never handed out.
func (f *Folder) Shadowed() []string {
	var held []string
	for _, name := range FolderReserved() {
		if _, err := f.root.Lstat(name); err == nil {
			held = append(held, name)
		}
	}
	return held
}

// ServeHTTP answers one file of the folder, to GET and HEAD.
func (f *Folder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	told := Request{At: time.Now(), Command: "page " + r.URL.Path}
	status := http.StatusOK
	defer func() {
		if f.report != nil {
			told.Status = status
			told.Took = time.Since(told.At)
			f.report(told)
		}
	}()
	h := w.Header()
	h.Set("Content-Security-Policy", FolderPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "same-origin")
	// A changed file is seen at the next load: the browser asks each
	// time whether its copy is still the file.
	h.Set("Cache-Control", "no-cache")

	plain := func(code int, text string) {
		status = code
		h.Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(code)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, text+"\n")
		}
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.Set("Allow", "GET, HEAD")
		plain(http.StatusMethodNotAllowed, "A page is read with GET.")
		return
	}

	name, ok := folderName(r.URL.Path)
	if !ok {
		plain(http.StatusNotFound, "Nothing here.")
		return
	}
	file, err := f.root.Open(name)
	if err != nil {
		plain(http.StatusNotFound, "Nothing here.")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		plain(http.StatusNotFound, "Nothing here.")
		return
	}
	if info.IsDir() {
		// A folder inside the folder is asked for with a slash at its
		// end, so that what its page links to is found beside it.
		if _, err := fs.Stat(f.root.FS(), path.Join(name, FolderIndex)); err != nil {
			plain(http.StatusNotFound, "Nothing here.")
			return
		}
		status = http.StatusMovedPermanently
		h.Set("Location", r.URL.Path+"/")
		w.WriteHeader(status)
		return
	}
	kind, known := folderKind(name)
	if !known {
		plain(http.StatusNotFound, "Nothing here.")
		return
	}
	h.Set("Content-Type", kind)
	// A browser that holds the file asks whether it is still the file,
	// and is told so without it. A file is sent whole: a page is small,
	// and the standard library's way of sending a part of one would
	// cost the program 50 KB (ADR-0073, Performance).
	changed := info.ModTime().UTC().Truncate(time.Second)
	h.Set("Last-Modified", changed.Format(http.TimeFormat))
	if held, err := http.ParseTime(r.Header.Get("If-Modified-Since")); err == nil && !changed.After(held) {
		status = http.StatusNotModified
		w.WriteHeader(status)
		return
	}
	h.Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, file)
	}
}

// folderName turns the path of a request into the name of a file in the
// folder, and says whether it may name one at all. A path that ends in a
// slash names the index of that folder. A part that begins with a dot is
// never handed out, the folder's own .git or .env among them, and no
// part goes up: what is asked for is taken as written, never cleaned
// into something else.
func folderName(asked string) (string, bool) {
	if !strings.HasPrefix(asked, "/") || strings.ContainsAny(asked, "\\\x00") {
		return "", false
	}
	if strings.HasSuffix(asked, "/") {
		asked += FolderIndex
	}
	name := asked[1:]
	for _, part := range strings.Split(name, "/") {
		if part == "" || strings.HasPrefix(part, ".") {
			return "", false
		}
	}
	return name, true
}

// folderKind answers the type a file is sent as, by the ending of its
// name, and whether the folder hands out such a file at all. The type
// never comes from the machine's own tables, which differ from one
// machine to the next.
func folderKind(name string) (string, bool) {
	switch strings.ToLower(path.Ext(name)) {
	case ".html":
		return "text/html; charset=utf-8", true
	case ".css":
		return "text/css; charset=utf-8", true
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8", true
	case ".json":
		return "application/json; charset=utf-8", true
	case ".txt":
		return "text/plain; charset=utf-8", true
	case ".svg":
		return "image/svg+xml", true
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".ico":
		return "image/x-icon", true
	case ".woff2":
		return "font/woff2", true
	}
	return "", false
}
