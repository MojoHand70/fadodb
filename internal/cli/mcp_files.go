package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/mcp"
	"github.com/MojoHand70/fadodb/internal/store"
)

// The folder of "fadodb mcp --files FOLDER" (ADR-0064, ruling 4): the
// door reads the files it puts from it and writes the files it gets into
// it, and nowhere else. The folder is held as a root of the operating
// system's files: a path is taken within it, and neither "..", nor a
// whole path outside it, nor a link that points out of it, leads out.

// folder is the folder a door was given.
type folder struct {
	whole string // the whole path of the folder
	root  *os.Root
	c     *caller
}

// openFolder holds the folder named with --files, for the calls of c.
func openFolder(dir string, c *caller) (*folder, error) {
	whole, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(whole)
	if err != nil {
		return nil, err
	}
	return &folder{whole: whole, root: root, c: c}, nil
}

func (f *folder) Folder() string { return f.whole }

func (f *folder) close() { f.root.Close() }

// refuse is what the door refuses of a path, at the place in the
// arguments where it was given.
func refuse(message, hint string) []byte {
	return mcp.Refusal(store.CodeBadValue, "/path", message, hint)
}

// within answers a path as the root takes it: a path in the folder, or a
// whole path that lies in it. A path that leads out is refused.
func (f *folder) within(path string) (string, []byte) {
	local := filepath.FromSlash(path)
	if filepath.IsAbs(local) {
		rel, err := filepath.Rel(f.whole, filepath.Clean(local))
		if err != nil {
			rel = ".."
		}
		local = rel
	}
	if !filepath.IsLocal(local) {
		return "", refuse("path is "+strconv.Quote(path)+", which is not in the folder "+f.whole+": the door reads and writes files there and nowhere else",
			"give the path of a file in the folder, as scan-0045.pdf")
	}
	return local, nil
}

// cannot is the refusal when the folder does not let the door read or
// write a path: no such file, no such folder in it, a link that leads
// out of it, or no right to it.
func (f *folder) cannot(doing, path string, err error) []byte {
	var why string
	switch {
	case errors.Is(err, fs.ErrNotExist):
		why = "there is no such file or folder"
	case errors.Is(err, fs.ErrPermission):
		why = "the door may not"
	default:
		var p *fs.PathError
		if errors.As(err, &p) {
			err = p.Err
		}
		why = err.Error()
	}
	return refuse("the door cannot "+doing+" "+strconv.Quote(path)+" in the folder "+f.whole+": "+why,
		"give the path of a file in the folder, as scan-0045.pdf; a link that leads out of the folder is not followed")
}

// Put reads a file from the folder and sends its bytes to the server,
// as "fadodb file put" without a document does.
func (f *folder) Put(ctx context.Context, p mcp.PutFile) ([]byte, bool, error) {
	local, refusal := f.within(p.Path)
	if refusal != nil {
		return refusal, true, nil
	}
	file, err := f.root.Open(local)
	if err != nil {
		return f.cannot("read", p.Path, err), true, nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return f.cannot("read", p.Path, err), true, nil
	}
	if !info.Mode().IsRegular() {
		return refuse(strconv.Quote(p.Path)+" in the folder "+f.whole+" is not a file", "give the path of a file in the folder, as scan-0045.pdf"), true, nil
	}
	name := p.Name
	if name == "" {
		name = filepath.Base(local)
	}
	resp, err := f.c.quietly(ctx, http.MethodPost, "blobs?"+url.Values{"name": {name}}.Encode(), file, info.Size(), p.MediaType)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	return heard(resp, "the answer to the file")
}

// Get opens a file through the document that holds it, as GET /v1/file,
// and writes it into the folder, over no other file. What is written is
// left only when it is whole and has the hash asked for.
func (f *folder) Get(ctx context.Context, g mcp.GetFile) ([]byte, bool, error) {
	// A path that is given is looked at before anything is asked of the
	// server: a caller who gave a wrong one is told so at once.
	local := ""
	if g.Path != "" {
		var refusal []byte
		if local, refusal = f.within(g.Path); refusal != nil {
			return refusal, true, nil
		}
		if refusal := f.free(local, g.Path); refusal != nil {
			return refusal, true, nil
		}
	}
	asked := url.Values{"class": {g.Class}, "id": {g.ID}, "hash": {g.Hash}}
	if g.Version != 0 {
		asked.Set("version", strconv.FormatInt(g.Version, 10))
	}
	if g.For != "" {
		asked.Set("for", g.For)
	}
	if g.Reason != "" {
		asked.Set("reason", g.Reason)
	}
	resp, err := f.c.quietly(ctx, http.MethodGet, "file?"+asked.Encode(), nil, -1, "")
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		answer, refused, err := heard(resp, "the file")
		if err == nil && !refused {
			err = fmt.Errorf("what answered at %s is not a FadoDB server: %s", resp.Request.URL.Redacted(), resp.Status)
		}
		return answer, refused, err
	}
	kind, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	name := ""
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		name = params["filename"]
	}
	given := g.Path
	if local == "" {
		// The file is written under its name, the last part of it: a name
		// is the words of whoever put the file.
		base := filepath.Base(filepath.FromSlash(strings.ReplaceAll(name, `\`, "/")))
		if name == "" || !filepath.IsLocal(base) {
			return refuse("the file has no name the door can write it under in the folder",
				"give path, the name to write it under, as scan-0045.pdf"), true, nil
		}
		local, given = base, base
		if refusal := f.free(local, given); refusal != nil {
			return refusal, true, nil
		}
	}
	out, err := f.root.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return f.taken(given), true, nil
	}
	if err != nil {
		return f.cannot("write", given, err), true, nil
	}
	sum := sha256.New()
	size, err := io.Copy(io.MultiWriter(out, sum), resp.Body)
	if closed := out.Close(); err == nil {
		err = closed
	}
	if err != nil {
		// What is left is no file: half of one is taken away.
		f.root.Remove(local)
		return nil, false, fmt.Errorf("the file broke off, and was not left in the folder: %v", err)
	}
	hash := hex.EncodeToString(sum.Sum(nil))
	if !strings.EqualFold(hash, g.Hash) || (resp.ContentLength >= 0 && size != resp.ContentLength) {
		f.root.Remove(local)
		return mcp.Refusal(store.CodeStorage, "", "the bytes the server sent are not the file asked for: their hash is "+hash+", and the file was not left in the folder",
			"an administrator checks the files of the database with check_blobs"), true, nil
	}
	// The facts in the order the server answers them when the file is
	// put.
	answer, err := command.Marshal(struct {
		Path string         `json:"path"`
		File store.FileInfo `json:"file"`
	}{given, store.FileInfo{Hash: hash, Name: name, MediaType: kind, Size: size}})
	return answer, false, err
}

// free refuses a path where a file already is: the door writes over
// none.
func (f *folder) free(local, given string) []byte {
	if _, err := f.root.Lstat(local); err == nil {
		return f.taken(given)
	}
	return nil
}

func (f *folder) taken(given string) []byte {
	return refuse("there is already a file at "+strconv.Quote(given)+" in the folder "+f.whole+", and the door writes over no file",
		"give another path, or move that file away first")
}
