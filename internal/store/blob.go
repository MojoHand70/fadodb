package store

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// Attachments (ADR-0033): a File value on a document is the facts of a
// file, hash, name, media type and size; the bytes live behind a BlobStore
// keyed by the SHA-256 of the content. The blob comes first: PutBlob
// stores the bytes and returns the fact, a commit that sets a File the
// store does not hold is refused, and a blob is served through a document
// that holds its hash, never by hash alone.

// DefaultMaxBlob is the blob size limit of a new database, 32 MiB.
const DefaultMaxBlob int64 = 32 << 20

// BlobStore holds the bytes of attachments by content hash (ADR-0033).
// The store is chosen when the database is created and recorded in its
// metadata; three come with the engine, "sqlite", a table in the database
// file, "dir", a directory beside it, and the embedder's own, given in
// Options.Blobs and recorded as "external".
type BlobStore interface {
	// Put stores a stream and returns its hash and size, hashing while it
	// writes. It stops with an error when the stream exceeds max bytes,
	// before anything is stored. A put of a hash already held is a no-op.
	Put(ctx context.Context, r io.Reader, max int64) (hash string, size int64, err error)
	// Open opens a blob as a stream with its size, or reports that it is
	// not held.
	Open(ctx context.Context, hash string) (io.ReadCloser, int64, error)
	// Stat reports whether a hash is held and how large the blob is.
	Stat(ctx context.Context, hash string) (size int64, ok bool, err error)
	// Delete removes a blob; removing one that is not held is no error.
	Delete(ctx context.Context, hash string) error
}

// BlobLister is a BlobStore that can enumerate its hashes, which
// CheckBlobs needs; the engine's two stores can.
type BlobLister interface {
	List(ctx context.Context, fn func(hash string) error) error
}

// ErrBlobTooLarge is what a BlobStore's Put returns past the limit.
var ErrBlobTooLarge = errors.New("the blob exceeds the database's limit")

// ErrNoBlob is what a BlobStore's Open returns for a hash it does not
// hold.
var ErrNoBlob = errors.New("the blob store does not hold the hash")

// FileInfo is the fact of a file as a document holds it (ADR-0032).
type FileInfo struct {
	Hash      string `json:"hash"`
	Name      string `json:"name,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Size      int64  `json:"size"`
}

// hashingReader hashes what passes through it and stops past a limit.
type hashingReader struct {
	r    io.Reader
	h    hashWriter
	n    int64
	max  int64
	over bool
}

type hashWriter interface {
	io.Writer
	Sum(b []byte) []byte
}

func (h *hashingReader) Read(p []byte) (int, error) {
	n, err := h.r.Read(p)
	if n > 0 {
		h.h.Write(p[:n])
		h.n += int64(n)
		if h.n > h.max {
			h.over = true
			return n, ErrBlobTooLarge
		}
	}
	return n, err
}

func (h *hashingReader) hash() string { return hex.EncodeToString(h.h.Sum(nil)) }

// sqliteBlobs keeps blobs in the blobs table of the database file.
type sqliteBlobs struct{ s *Store }

func (b sqliteBlobs) Put(ctx context.Context, r io.Reader, max int64) (string, int64, error) {
	hr := &hashingReader{r: r, h: sha256.New(), max: max}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, hr); err != nil {
		return "", 0, err
	}
	hash := hr.hash()
	s := b.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", 0, errf(CodeClosed, "", "the store is closed")
	}
	if _, err := s.writer.ExecContext(ctx, "INSERT INTO blobs (hash, size, bytes, put_at) VALUES (?, ?, ?, ?) ON CONFLICT (hash) DO NOTHING", hash, hr.n, buf.Bytes(), s.now().Unix()); err != nil {
		return "", 0, storageErr("store the blob", err)
	}
	return hash, hr.n, nil
}

func (b sqliteBlobs) Open(ctx context.Context, hash string) (io.ReadCloser, int64, error) {
	var size int64
	var data []byte
	err := b.s.db.QueryRowContext(ctx, "SELECT size, bytes FROM blobs WHERE hash = ?", hash).Scan(&size, &data)
	if err == sql.ErrNoRows {
		return nil, 0, ErrNoBlob
	}
	if err != nil {
		return nil, 0, storageErr("read the blob", err)
	}
	return io.NopCloser(bytes.NewReader(data)), size, nil
}

func (b sqliteBlobs) Stat(ctx context.Context, hash string) (int64, bool, error) {
	var size int64
	err := b.s.db.QueryRowContext(ctx, "SELECT size FROM blobs WHERE hash = ?", hash).Scan(&size)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, storageErr("read the blob", err)
	}
	return size, true, nil
}

func (b sqliteBlobs) Delete(ctx context.Context, hash string) error {
	s := b.s
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.writer.ExecContext(ctx, "DELETE FROM blobs WHERE hash = ?", hash)
	return storageErr("delete the blob", err)
}

func (b sqliteBlobs) List(ctx context.Context, fn func(hash string) error) error {
	rows, err := b.s.db.QueryContext(ctx, "SELECT hash FROM blobs ORDER BY hash")
	if err != nil {
		return storageErr("list the blobs", err)
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return storageErr("list the blobs", err)
		}
		if err := fn(hash); err != nil {
			return err
		}
	}
	return storageErr("list the blobs", rows.Err())
}

// dirBlobs keeps blobs as files under <database>.blobs/<hh>/<hash>, each
// written under a temporary name, fsynced and renamed.
type dirBlobs struct{ dir string }

func (d dirBlobs) path(hash string) string {
	if len(hash) < 2 {
		return filepath.Join(d.dir, "xx", hash)
	}
	return filepath.Join(d.dir, hash[:2], hash)
}

func (d dirBlobs) Put(ctx context.Context, r io.Reader, max int64) (string, int64, error) {
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(d.dir, "put-*.tmp")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(tmp.Name())
	hr := &hashingReader{r: r, h: sha256.New(), max: max}
	if _, err := io.Copy(tmp, hr); err != nil {
		tmp.Close()
		return "", 0, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	hash := hr.hash()
	target := d.path(hash)
	if _, err := os.Stat(target); err == nil {
		return hash, hr.n, nil // held already: a no-op
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return "", 0, err
	}
	return hash, hr.n, nil
}

func (d dirBlobs) Open(ctx context.Context, hash string) (io.ReadCloser, int64, error) {
	f, err := os.Open(d.path(hash))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrNoBlob
	}
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, fi.Size(), nil
}

func (d dirBlobs) Stat(ctx context.Context, hash string) (int64, bool, error) {
	fi, err := os.Stat(d.path(hash))
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return fi.Size(), true, nil
}

func (d dirBlobs) Delete(ctx context.Context, hash string) error {
	err := os.Remove(d.path(hash))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (d dirBlobs) List(ctx context.Context, fn func(hash string) error) error {
	shards, err := os.ReadDir(d.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, sh := range shards {
		if !sh.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(d.dir, sh.Name()))
		if err != nil {
			return err
		}
		for _, f := range files {
			if f.IsDir() || strings.HasSuffix(f.Name(), ".tmp") {
				continue
			}
			if err := fn(f.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}

// Blobs is the database's blob store.
func (s *Store) Blobs() BlobStore { return s.blobs }

// MaxBlob is the database's blob size limit in bytes (ADR-0033).
func (s *Store) MaxBlob() int64 { return s.maxBlob }

// BlobStoreKind is the kind of blob store the database records: sqlite,
// dir or external.
func (s *Store) BlobStoreKind() string { return s.blobKind }

// PutBlob stores the bytes of a file and returns the File fact to commit
// (ADR-0033): the hash, the name given, the media type given or sniffed
// from the first bytes when none was, and the size. The fact is a Values
// ready for an object<File> variable or an element of rows<File>. A
// stream past the database's limit is the error blob_too_large, and
// nothing is stored.
func (s *Store) PutBlob(ctx context.Context, name, mediaType string, r io.Reader, opts ...Option) (Values, error) {
	_ = apply(opts)
	br := bufio.NewReaderSize(r, 512)
	if mediaType == "" {
		head, _ := br.Peek(512)
		mediaType = http.DetectContentType(head)
	}
	hash, size, err := s.blobs.Put(ctx, br, s.maxBlob)
	if err != nil {
		if errors.Is(err, ErrBlobTooLarge) {
			return nil, errf(CodeBlobTooLarge, "", "the file exceeds the database's blob limit of %d bytes (ADR-0033)", s.maxBlob)
		}
		var se *Error
		if errors.As(err, &se) {
			return nil, se
		}
		return nil, &Error{Code: CodeStorage, Message: "store the blob: " + err.Error()}
	}
	return Values{"hash": hash, "name": name, "media_type": mediaType, "size": size}, nil
}

// OpenFile opens a file a document holds: the hash must be the hash of a
// File of the document's current version, or of the version AtVersion
// names. It returns the stream and the fact as the document holds it.
// The caller has to see the document, by its read rule as it is now, and
// the variable that holds the file, by its read right in the state of
// the version asked (ADR-0049, item 11): a document that is hidden does
// not exist, and a file in a hidden variable is not held.
func (s *Store) OpenFile(ctx context.Context, class, id, hash string, opts ...Option) (io.ReadCloser, *FileInfo, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, nil, e
	}
	o, e := asked(opts)
	if e != nil {
		return nil, nil, e
	}
	if seen, err := s.sees(ctx, c, id, o); err != nil {
		return nil, nil, err
	} else if !seen {
		return nil, nil, errf(CodeUnknownDocument, "", "no document %s of class %s at that version", id, c.cls.Name)
	}
	cur := c
	var row *docRow
	var err error
	if o.version > 0 {
		row, err = c.scanRow(c.r.getVersion.QueryRowContext(ctx, id, o.version), true)
	} else {
		row, err = c.scanRow(c.r.getCurrent.QueryRowContext(ctx, id), false)
	}
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, errf(CodeUnknownDocument, "", "no document %s of class %s at that version", id, c.cls.Name)
		}
		return nil, nil, storageErr("read the document", err)
	}
	if c, row, err = s.underVersion(ctx, c, row, o.version > 0); err != nil {
		return nil, nil, err
	}
	version := row.version
	// hidden says which variables of the version the caller may not read:
	// the rights table of the current class, by name, over that row
	// (ADR-0041, item 4).
	hidden := s.hiddenFor(cur, c, row, o)
	var info *FileInfo
	for _, fp := range c.files {
		if hidden[strings.ToUpper(strings.SplitN(fp.path, ".", 2)[0])] {
			continue
		}
		if info = fileOfPairs([]filePair{fp}, row.vals, hash); info != nil {
			break
		}
	}
	if info == nil {
		kids, err := c.loadChildren(ctx, id, version, nil, false)
		if err != nil {
			return nil, nil, err
		}
		for ch, rows := range kids {
			if hidden[strings.ToUpper(ch.v.Name)] {
				continue
			}
			for _, r := range rows {
				if info = ch.fileOf(r, hash); info != nil {
					break
				}
			}
			if info != nil {
				break
			}
		}
	}
	if info == nil {
		return nil, nil, errf(CodeUnknownBlob, "", "the document %s of class %s does not hold a file %s", id, c.cls.Name, hash)
	}
	rc, _, err := s.blobs.Open(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrNoBlob) {
			return nil, nil, errf(CodeUnknownBlob, "", "the blob store does not hold %s", hash)
		}
		return nil, nil, &Error{Code: CodeStorage, Message: "open the blob: " + err.Error()}
	}
	return rc, info, nil
}

// OpenBlob opens a blob by hash alone, which a content administrator may
// (ADR-0033); anyone else goes through OpenFile.
func (s *Store) OpenBlob(ctx context.Context, hash string, opts ...Option) (io.ReadCloser, int64, error) {
	o := apply(opts)
	if !o.principal.content() {
		return nil, 0, hintf(CodeNotAllowed, "", "a blob is served through a document", "use OpenFile with the class and the document that hold it; a content administrator may open a blob by hash")
	}
	rc, size, err := s.blobs.Open(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrNoBlob) {
			return nil, 0, errf(CodeUnknownBlob, "", "the blob store does not hold %s", hash)
		}
		return nil, 0, &Error{Code: CodeStorage, Message: "open the blob: " + err.Error()}
	}
	return rc, size, nil
}

// CheckBlobs reads every held blob, recomputes its hash and returns the
// hashes whose bytes do not match, and how many it checked (ADR-0033);
// a restore from an altered backup is found this way. A blob store that
// cannot list its hashes is the error not_supported.
func (s *Store) CheckBlobs(ctx context.Context, opts ...Option) (mismatched []string, checked int, err error) {
	if _, e := s.worker(workOperate, opts); e != nil {
		return nil, 0, e
	}
	lister, ok := s.blobs.(BlobLister)
	if !ok {
		return nil, 0, errf(CodeNotSupported, "", "the %s blob store cannot list its blobs", s.blobKind)
	}
	err = lister.List(ctx, func(hash string) error {
		rc, _, err := s.blobs.Open(ctx, hash)
		if err != nil {
			return err
		}
		defer rc.Close()
		h := sha256.New()
		if _, err := io.Copy(h, rc); err != nil {
			return err
		}
		checked++
		if hex.EncodeToString(h.Sum(nil)) != hash {
			mismatched = append(mismatched, hash)
		}
		return nil
	})
	return mismatched, checked, err
}

// filePair is where a File object keeps its hash and its size among the
// leaves of a row: the leaves' indices, and the path for messages.
type filePair struct {
	hash, size, name, media int
	path                    string
}

// filePairs finds the File objects below a type at a prefix: an object
// variable or field of the File schema, or rows of it, whose row is the
// File and whose leaves sit at the root, with an empty prefix.
func filePairs(prefix string, t formula.Type, index map[string]int) []filePair {
	if t.Kind != formula.KindObject && t.Kind != formula.KindRows {
		return nil
	}
	join := func(f string) string {
		if prefix == "" {
			return f
		}
		return prefix + "." + f
	}
	if strings.EqualFold(t.Schema(), "File") {
		at := func(f string) int {
			i, ok := index[strings.ToUpper(join(f))]
			if !ok {
				return -1
			}
			return i
		}
		return []filePair{{hash: at("hash"), size: at("size"), name: at("name"), media: at("media_type"), path: prefix}}
	}
	var out []filePair
	for _, f := range t.Fields() {
		out = append(out, filePairs(join(f.Name), f.Type, index)...)
	}
	return out
}

// fileOf finds the fact of the file with the hash among the leaves.
func fileOfPairs(pairs []filePair, vals []formula.Value, hash string) *FileInfo {
	for _, fp := range pairs {
		if fp.hash < 0 || vals[fp.hash].Str() != hash {
			continue
		}
		info := &FileInfo{Hash: hash}
		if fp.name >= 0 {
			info.Name = vals[fp.name].Str()
		}
		if fp.media >= 0 {
			info.MediaType = vals[fp.media].Str()
		}
		if fp.size >= 0 {
			info.Size = vals[fp.size].Mant()
		}
		return info
	}
	return nil
}

func (ch *child) fileOf(vals []formula.Value, hash string) *FileInfo {
	return fileOfPairs(ch.files, vals, hash)
}

// checkFiles refuses a commit that sets a File the blob store does not
// hold, or whose size differs from the stored size (ADR-0033): the
// variables assigned in this commit, and the composites given.
func (s *Store) checkFiles(ctx context.Context, c *class, row *docRow, dirty []bool, kids map[*child][]childRow) *Error {
	for _, fp := range c.files {
		cv := c.byName[strings.ToUpper(strings.SplitN(fp.path, ".", 2)[0])]
		if cv == nil || !dirty[cv.idx] {
			continue
		}
		if err := s.checkFile(ctx, row.vals, fp, fp.path); err != nil {
			return err
		}
	}
	for ch, rows := range kids {
		for i, r := range rows {
			for _, fp := range ch.files {
				path := fmt.Sprintf("%s[%d]", ch.v.Name, i)
				if fp.path != "" {
					path += "." + fp.path
				}
				if err := s.checkFile(ctx, r, fp, path); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Store) checkFile(ctx context.Context, vals []formula.Value, fp filePair, path string) *Error {
	if fp.hash < 0 {
		return nil
	}
	hash := vals[fp.hash]
	if hash.IsBlank() {
		return nil
	}
	size, ok, err := s.blobs.Stat(ctx, hash.Str())
	if err != nil {
		return &Error{Code: CodeStorage, Variable: path, Message: "check the blob: " + err.Error()}
	}
	if !ok {
		return hintf(CodeUnknownBlob, path, "the blob store does not hold "+hash.Str(), "put the file first with PutBlob and commit the fact it returns")
	}
	if fp.size >= 0 && (vals[fp.size].IsBlank() || vals[fp.size].Mant() != size) {
		return errf(CodeUnknownBlob, path, "the size %s differs from the stored %d bytes of %s", formula.Render(vals[fp.size]), size, hash.Str())
	}
	return nil
}
