# Files

A document often comes with a file: the PDF of an invoice, the photo
of a receipt, a signed contract. In FadoDB a file on a document is a
value like any other. The document holds the **facts** of the file,
its hash, its name, its kind and its size, in an object of the schema
`File`. The **bytes** are kept beside, under their hash. So attaching
a file, replacing it and taking it off are changes of the document:
each makes a version and an event, and a guard, a right and a formula
see the file as they see any value.

This chapter is the reference for files. A test plays what stands here
on each push.

## Two steps

| Step | The caller | The engine |
|---|---|---|
| 1 | puts the bytes, with a name | keeps them under their hash and answers with the facts of the file |
| 2 | sets a variable of the schema `File` to those facts, in a create or a change | checks that it keeps bytes of that hash and that size, and commits |

The bytes do nothing until a document names them.

## The facts

Ola puts the scan of a taxi receipt, a PDF of 24 bytes, under the
name `taxi.pdf`. The engine answers:

<!-- the facts of the file -->
```jsonc
{
  "hash": "d6940f282d8cc36b76fecd3fed829379f9ef49bb2c14af90e97e9bd84761a8d7",
  "media_type": "application/pdf",
  "name": "taxi.pdf",
  "size": 24
}
```

<!-- the facts -->
| Fact | What it is | Who says it |
|---|---|---|
| `hash` | the SHA-256 of the bytes, as 64 letters and digits | the engine |
| `size` | the number of bytes | the engine |
| `media_type` | the kind of file, such as `application/pdf` | the caller; when the caller says nothing, the engine reads it from the first bytes |
| `name` | the name the file was given | the caller |

- **The hash is the content.** The same bytes put twice have the same
  hash and are kept once, whatever they are named.
- **The engine checks the hash and the size**, and takes the name and
  the kind as the caller's words.

## The class

A receipt with its scan, further pages, and a working paper that only
accounting writes and sees.

```json
{
  "name": "Receipt",
  "title": "\"Receipt for \" & what",
  "variables": {
    "what":  {"type": "text", "required": true},
    "scan":  {"type": "object", "schema": "File"},
    "pages": {"type": "rows", "schema": "File"},
    "audit": {"type": "object", "schema": "File"},
    "large": {"formula": "scan.size > 1000000"}
  },
  "states": {"initial": "open", "list": ["open", "filed"], "final": ["filed"]},
  "transitions": [
    {"name": "file", "from": "open", "to": "filed", "guard": "NOT ISBLANK(scan.hash)"}
  ],
  "access": {
    "read":   "CREATEDBY() = USER() OR INGROUP(\"ACCOUNTING\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER() OR INGROUP(\"ACCOUNTING\")",
    "delete": "CREATEDBY() = USER()"
  },
  "rights": {
    "*": {
      "*":     {"write": "CREATEDBY() = USER()"},
      "audit": {"write": ["ACCOUNTING"], "read": ["ACCOUNTING"]}
    }
  }
}
```

One file is a variable of the type `object` with the schema `File`.
Several files are `rows` of that schema. The schema is one of those
the engine brings; see [Schemas and objects](schemas-and-objects.md).

## What it does

Ola files a taxi receipt. Anna is in the group `ACCOUNTING`. Piotr is
a colleague with no part in it. Celina is a content administrator and
Adam an administrator.

<!-- what it does -->
| Step | Who | Does | Answer |
|---|---|---|---|
| 1 | Ola | puts the scan, `taxi.pdf` | the facts above |
| 2 | Ola | creates a receipt with those facts as its `scan` | allowed, version 1 |
| 3 | Ola | creates a receipt with the facts of a file nobody put | refused, `unknown_blob`, variable `scan` |
| 4 | Ola | creates a receipt with the hash of her scan and another size | refused, `unknown_blob`, variable `scan` |
| 5 | Ola | opens the scan | the 24 bytes |
| 6 | Piotr | opens the scan | refused, `unknown_document` |
| 7 | Piotr | opens the bytes by their hash alone | refused, `not_allowed` |
| 8 | Anna | attaches a working paper as `audit` | allowed, version 2 |
| 9 | Ola | opens the working paper | refused, `unknown_blob` |
| 10 | Anna | opens the working paper | the 18 bytes |
| 11 | Ola | replaces the scan with a better one, `taxi-better.pdf` | allowed, version 3 |
| 12 | Ola | opens the first scan | refused, `unknown_blob` |
| 13 | Ola | opens the first scan as of version 1 | the 24 bytes |
| 14 | Ola | takes the scan off | allowed, version 4 |
| 15 | Ola | files the receipt | refused, `guard_false` |
| 16 | Celina | opens the first scan by its hash alone | the 24 bytes |
| 17 | Adam | checks the files | 3 checked, 0 wrong |

What the history keeps:

<!-- the history -->
| Version | Made by | `scan` |
|---|---|---|
| 1 | ola | taxi.pdf |
| 2 | anna | taxi.pdf |
| 3 | ola | taxi-better.pdf |
| 4 | ola | blank |

## Attaching

- **The file comes first.** A document cannot name bytes the engine
  does not keep: steps 3 and 4 are refused with `unknown_blob`.
- **A file with no hash is checked for nothing.** The facts are an
  object, and an object with a name and no hash is accepted. So ask
  for the hash where a file must be there: `NOT ISBLANK(scan.hash)`,
  as the guard of `file` does.
- **Every attach is a version.** Replacing a file and taking it off
  are versions too, and the history shows which file each version
  held.
- **Who may attach** is who may write the variable: `access.update`
  and `rights`, as for any value. Anna may write `audit` and nothing
  else.
- **A formula reads the facts**, one field at a time: `scan.size`,
  `scan.media_type`, `scan.name`. The files of `pages` cannot be read
  in a formula yet, as no rows can.

## Opening

A file is opened through a document: the caller names the class, the
document and the hash.

<!-- opening -->
| The caller | Is answered |
|---|---|
| sees the document, and may read the variable that holds the file | the bytes, and the facts as the document holds them |
| may not see the document | `unknown_document` |
| sees the document, and may not read the variable | `unknown_blob` |
| names a hash the document does not hold in this version | `unknown_blob` |
| names a version, and the document held the hash then | the bytes |

- **The hash alone opens nothing.** Knowing a hash is no right to the
  bytes. Only a content administrator may open bytes by their hash,
  as in step 16.
- **The same bytes on two documents** are served to whoever may read
  either of them, through that one.
- **A caller who may not see is told what a stranger is told**: that
  there is no such document, or that it holds no such file.
- **A deleted document keeps its files.** Its versions are still
  there, and a file is opened as of one of them by whoever may read
  the document.

## Who may

<!-- who may -->
| To | A caller needs |
|---|---|
| put bytes | nothing |
| attach, replace or take off a file | to write the variable, as for any value |
| open a file | to see the document and to read the variable |
| open bytes by their hash alone | the system group `content-administrators` |
| check the files | the system group `administrators` |

> **Putting asks for nobody.** The engine keeps bytes for any call,
> one that names no caller included. The design asks for a caller
> ([ADR-0033](../adr/0033-attachments-as-file-values-behind-a-blob-store.md));
> that is not built. Bytes that no document names can be opened by
> nobody but a content administrator, and they take room.

## Where the bytes are kept

The bytes are in a **store**, chosen when the database is created,
with the option `BlobStore`; see
[Opening a database](opening-a-database.md).

<!-- the stores -->
| Store | The bytes are | Choose it when |
|---|---|---|
| `sqlite` | in the database file itself | one file should hold everything, and a backup is one copy. It is the store when none is named |
| `dir` | in a directory beside the database file, one file for each hash | the files are large, or should be seen on disk |
| `external` | where the host program keeps them | the host program has a store of its own, and gives it to the engine when it opens the database |

- **The store stays.** A database that was created with one store is
  not opened with another.
- **In the `dir` store** the bytes of a hash are in the file
  `<database>.blobs/<the first two letters of the hash>/<hash>`.
- **A backup** of a database with the `dir` store is the database file
  and that directory; see [Running it](running-it.md#what-a-copy-is).

## How large a file may be

A database has a limit for one file, 32 MiB when none is given. Bytes
past the limit are refused with `blob_too_large`, and nothing is
kept. The limit may be raised when the database is opened, and never
lowered.

## Checking the files

A check reads all the bytes the database keeps, works out the hash of
each again, and names the ones that do not match: a file that was
changed on disk, or a backup that was damaged. An administrator runs
it, as the command `check_blobs`; on the command line `fadodb check`
does the same for a database file.

A store of the host program that cannot list what it keeps cannot be
checked this way, and the check is refused with `not_supported`.

## Bytes are not commands

Everything else a caller does is a command in JSON; see
[The commands](commands.md). Bytes are not JSON. Putting and opening a
file are calls of the Go package, `PutBlob`, `OpenFile` and
`OpenBlob`, and over HTTP they have addresses of their own beside the
commands: see [Files over HTTP](commands.md#files-over-http).

## What is refused

<!-- what is refused -->
| The caller | Code | The refusal names |
|---|---|---|
| puts bytes past the limit of the database | `blob_too_large` | |
| names a hash the engine does not keep, in a document | `unknown_blob` | the variable, as `scan` |
| names a hash with another size than the bytes have | `unknown_blob` | the variable, as `scan` |
| names a hash the engine does not keep, in a row of files | `unknown_blob` | the row, as `pages[0]` |
| opens a file of a document they may not see | `unknown_document` | |
| opens a file the document does not hold, or holds in a variable they may not read | `unknown_blob` | |
| opens bytes by their hash alone, and is no content administrator | `not_allowed` | |
| checks the files, and is no administrator | `forbidden` | `/system/administrators` |
| opens a database with another store than it was created with | `bad_value` | |
| opens a database with a lower limit than it has | `bad_value` | |

## Not here yet

- Removing bytes. Bytes stay when a file is replaced, taken off, or
  its document deleted, and when no document ever named them. A
  retention command that removes what no version holds is designed and
  not built.
- A caller for putting bytes.
- Copying the bytes of a database to another store.
- Reading the files of `rows` in a formula, such as their number.
- Previews, thumbnails and the text of a file. They are the host
  program's, which may attach them as further files.
