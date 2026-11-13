# ADR-0033: Attachments are File values; the bytes live behind a blob store

- Status: Accepted; amends ADR-0011, ADR-0018, ADR-0021; amended by ADR-0049 (`OpenFile` asks the read rule and the right), ADR-0052 (who opens a blob by its hash)
- Date: 2026-11-07

## Context

ADR-0018 planned attachments after 1.0 as side tables keyed by class and
document id with their own events. The need came sooner. The first embedder
receives documents as files: a PDF invoice, a bank statement, a photo of a
receipt. It keeps the bytes in an append-only table keyed by content hash,
puts the file's facts, hash, name, media type and size, into its event and
never the bytes, caps a file at 32 MiB, and learned that a hash alone must
never open a file: a fact naming another company's hash once did. A process
wants guards on files: no approval without the scan.

With ADR-0031 a file's facts are an object, which answers what an
attachment is. What remained was where the bytes live. One database file is
the simplest deployment and backup. Blobs in a columnar engine are a poor
fit, and the Phase 2 benchmark may yet make DuckDB the primary store
(ADR-0017). An embedder with a blob store of its own wants no second copy
of every PDF. On 2026-11-07 the owner decided that attaching a file is a
commit and a new version.

## Decision

1. **An attachment is a `File` value** (ADR-0032) in a variable of type
   `object<File>` or `rows<File>`. Attaching, replacing and removing a file
   are commits under ADR-0015, each a new version with a history row. The
   facts are typed columns like any object's, so guards, rules, rights and
   formulas see them: `NOT ISBLANK(scan)` is a guard. There is no side
   table and no new event kind. Comments stay planned after 1.0.

2. **The hash is the content.** `hash` is the lowercase hexadecimal SHA-256
   of the bytes, `size` the byte count, `media_type` the type the file was
   given or, when none was, the type sniffed from its first bytes, and
   `name` the name it was given. Blobs are immutable and shared: two
   documents holding the same hash share one blob.

3. **The file comes first.** A commit that sets a `File` whose `hash` the
   database's blob store does not hold, or whose `size` differs from the
   stored size, is refused with the error `unknown_blob` naming the
   variable. `media_type` and `name` are the client's facts and are not
   checked. A blank `File` is checked for nothing.

4. **A `BlobStore` interface** in the Go API with four operations: put a
   stream and return its hash and size, hashing while writing and stopping
   at the limit of item 5; open a blob by hash as a stream with its size;
   test whether a hash is held and how large it is; delete a hash. A put of
   a hash already held is a no-op. Three stores ship:
   - `sqlite`: a `blobs` table (`hash` primary key, `size`, `bytes`) in the
     primary database file, the default. One file holds everything, and a
     backup is a copy.
   - `dir`: a directory beside the database file,
     `<database>.blobs/<hh>/<hash>`, each blob written under a temporary
     name, fsynced and renamed. For large files, and the default should
     DuckDB become the primary store.
   - the embedder's: the host registers an implementation in the Go API, as
     it registers executors under ADR-0027. The first embedder's hash-keyed
     table is one.

   The store is chosen when the database is created and recorded in its
   metadata; a command copies every referenced blob to another store. An
   S3 store, if wanted, is a separate module. The core gains no dependency:
   `crypto/sha256` and `net/http.DetectContentType` are in the standard
   library.

5. **`MaxBlob` is a database limit** (amends ADR-0021). It is chosen when
   the database is created, the default is 32 MiB, it is kept in the
   metadata beside `MaxText`, and it may be raised, never lowered. A put
   stops at the limit with `blob_too_large` before anything is stored. The
   limit is the database's, so every store enforces the same one. The
   number of files on a document is bounded by the element caps of
   ADR-0013 on `rows<File>`.

6. **Putting and serving.** The Go API gains `PutBlob`, taking a
   principal, a name, a media type and a reader and returning the `File`
   fact to commit, and `OpenFile`, taking a principal, a class, a document
   id, a hash and an optional version and returning the stream. A blob is
   served through a document, never by hash alone: the caller must pass
   the document's read rule (ADR-0010), the hash must be a `File` leaf of
   the document's current version or of the version named, and that
   variable's read right (ADR-0028) must be TRUE for the caller in that
   state. An administrator may open any blob by hash. Putting a blob needs
   a principal and nothing else, since a blob is inert until a commit
   names it; a put that no commit names within the grace period of item 7
   is removed. Over HTTP (Phase 6) `POST /v1/blobs` takes the body with its
   `Content-Type` and a file name and answers with the `File` fact, and
   `GET /v1/docs/{class}/{id}/files/{hash}` streams with the hash as ETag.
   This is the one place bytes travel outside the JSON command set of
   ADR-0011, which otherwise carries `File` facts only. The C ABI (Phase 9)
   gains a put and an open entry point.

7. **Integrity and retention.** `fadodb check` recomputes every held
   blob's hash and reports a mismatch, so a restore from an altered backup
   is found. The retention command of ADR-0006 and ADR-0014 removes a blob
   only when no current row and no history row in the database holds its
   hash and its put is older than the grace period, 24 hours by default;
   it lists what it removes and never runs on its own. Archiving history
   versions to a file archives their blobs with them. Deleting a document
   removes its current row and keeps its history, so its blobs stay until
   retention.

8. **Phases.** Phase 2 delivers the `File` schema, the interface, the
   `sqlite` and `dir` stores, `PutBlob` and `OpenFile` in the Go API, the
   `unknown_blob` check, `MaxBlob` in the metadata and the hash check in
   `fadodb check`. Phase 5 adds the read rule and the field right to
   `OpenFile`, Phase 6 the routes, Phase 9 the C ABI entries. Thumbnails,
   previews and extracted text are the embedder's; it attaches them as
   further `File` values.

## Consequences

- Attachments arrive with the document model and reuse all of it: a
  document's history shows which files it held at every version, and a
  guard can require one.
- Every attach is a version. A document with many files and frequent
  attaches grows history rows, and a `rows<File>` is copied per version
  like invoice lines, four small columns per file.
- With the `sqlite` store the database file grows by the blobs and a backup
  stays one file. The `dir` store keeps the database small and the files
  visible on disk.
- The hash is never a capability. The same hash held by two documents is
  served to whoever may read either one.
- ADR-0011 gains one exception for bytes, ADR-0021 a second database limit,
  and ADR-0018's side-object line shrinks to comments. The "after 1.0" line
  of the architecture page goes.
- Phase 8's projection sees `File` leaves as columns; DuckDB never sees
  bytes.
- The first embedder keeps one copy of every file: its store is registered,
  and FadoDB holds the hash.

## Performance

- A put hashes while it writes, at SHA-256 speed; a 32 MiB file takes tens
  of milliseconds and never runs inside a commit.
- A commit that sets a `File` adds one primary-key lookup in the blob store
  per file set, inside the 2 ms p99 of ADR-0006; the Phase 2 commit
  benchmark includes a class with `object<File>` and `rows<File>`.
- Serving streams from the store; the `dir` store streams the file, the
  `sqlite` store reads the blob in one statement.
- Opening a database is unchanged; the store is a metadata row.
