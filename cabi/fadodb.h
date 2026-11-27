/*
 * fadodb.h - FadoDB in a program of any language (ADR-0101).
 *
 * The library is the engine of fadodb-core: libfadodb-core.so on Linux,
 * libfadodb-core.dylib on macOS, fadodb-core.dll on Windows. A program
 * opens a database file, sends it commands and closes it:
 *
 *     char *refusal;
 *     fado_db db = fado_open("books.fado", "{}", &refusal);
 *     char *answer = fado_exec(db, "{\"id\": \"ann\", \"groups\": [\"finance\"]}",
 *         "transition", "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"transition\": \"approve\"}");
 *     ...
 *     fado_free(answer);
 *     fado_close(db, &refusal);
 *
 * A command is a name and one JSON object, and its answer is one JSON
 * object: the commands of the command set (ADR-0055), the same as a
 * server takes at POST /v1/<command>, answered the same. An answer that
 * has the member "error" at its top is a refusal; its "code" is what a
 * program branches on.
 *
 * Text in and out is UTF-8, ended by a zero byte. What a program gives
 * is copied before the call returns, and may be freed at once. What the
 * library answers is the program's to give back with fado_free, once.
 *
 * Every function may be called from any thread, at the same time as any
 * other, on one database or on several.
 */
#ifndef FADODB_H
#define FADODB_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* A database the library opened: a number, 0 for none. A number is
 * given once in the life of a process; after fado_close it reaches no
 * database, and a command sent to it is refused with "closed". */
typedef uint64_t fado_db;

/* Opens the database file at path, and makes it when it does not exist.
 * options is a JSON object, or NULL or "" for {}:
 *
 *     {"timezone": "Europe/Warsaw", "synchronous": "FULL",
 *      "max_question_seconds": 5, "max_text": 100000,
 *      "max_indexed_rows": 100000, "max_blob_bytes": 33554432,
 *      "blob_store": "sqlite", "host_answers_for": false,
 *      "outbox": {"manual": false, "workers": 4, "max_attempts": 10,
 *                 "backoff_seconds": 1},
 *      "outbound": {"hosts": ["hooks.acme.example"],
 *                   "connections": {"slack": ["slack.com"]}},
 *      "secrets_dir": "/etc/books/secrets"}
 *
 * Every member may be left out. outbound lists the hosts the declared
 * actions of the database may call, in the words of the settings of
 * fadodb serve; without it the database sends no request. The secret
 * token of the connection slack is then the file slack/token in
 * secrets_dir, or the variable FADODB_SECRET_SLACK__TOKEN of the
 * program's environment, read again for every request. Answers the database, or 0 when it was
 * not opened; then *refusal, when refusal is not NULL, is set to the
 * refusal, {"error": {...}}, to be given back with fado_free. */
fado_db fado_open(const char *path, const char *options, char **refusal);

/* Runs one command on the database as the caller, and answers it.
 *
 * caller is who the program says is calling, as a JSON object with the
 * members a user has in the command set: {"id": "ann", "kind": "person",
 * "groups": ["finance"], "system_groups": ["designers"], "today":
 * "2026-11-27"}, each of which may be left out; NULL, "" and {} are
 * nobody. The engine trusts it as a Go program's Principal: the program
 * that loaded the library has said who it is. Whom the caller acts for
 * is the command's "for", which the engine checks.
 *
 * command is the name of the command, such as "create" or "transition",
 * and object its JSON object; NULL and "" are {}.
 *
 * Never NULL: the answer, or the refusal, as one JSON object, to be
 * given back with fado_free. A command that waits, wait_for_changes,
 * holds its thread while it waits. subscribe is refused: its answer is a
 * stream, and wait_for_changes answers the same events one call at a
 * time. */
char *fado_exec(fado_db db, const char *caller, const char *command, const char *object);

/* Closes the database: the commands that wait are answered at once, the
 * ones that run end first, and the file is closed. Answers 0, or -1 and
 * then *refusal, when refusal is not NULL, is set as fado_open sets it. */
int fado_close(fado_db db, char **refusal);

/* The files travel beside the commands, as over HTTP: their bytes are no
 * JSON. What a file is on a document, its facts, is a value like any
 * other, which a command commits. Each function takes the database, the
 * caller and one JSON object, as fado_exec does, and answers one JSON
 * object, never NULL, given back with fado_free.
 *
 * fado_put_file keeps length bytes and answers the facts of the file,
 * {"file": {"hash": "d694…", "name": "taxi.pdf", "media_type":
 * "application/pdf", "size": 48213}}, for a command to commit in a
 * variable of the schema File, as POST /v1/blobs does. object is
 * {"name": "taxi.pdf", "media_type": "application/pdf"}; both may be
 * left out. The bytes are read before the call returns. */
char *fado_put_file(fado_db db, const char *caller, const char *object, const void *bytes, size_t length);

/* fado_get_file reads a file a document holds, under the rules of the
 * document, as GET /v1/file does. object is {"class": "Receipt", "id":
 * "R-7", "hash": "d694…"}, with "version" for an earlier version of the
 * document, "for" for whom the caller acts and "reason" for a content
 * administrator who reads past a rule. It answers the facts of the file,
 * and sets *bytes and *length to its bytes, which fado_free gives back;
 * on a refusal *bytes is NULL. */
char *fado_get_file(fado_db db, const char *caller, const char *object, void **bytes, size_t *length);

/* fado_get_blob reads bytes by their hash alone, which the engine does
 * for a content administrator and nobody else, with a reason the log of
 * reads keeps, as GET /v1/blob does. object is {"hash": "d694…",
 * "reason": "…"}. It answers and sets the bytes as fado_get_file. */
char *fado_get_blob(fado_db db, const char *caller, const char *object, void **bytes, size_t *length);

/* Gives back what the library answered: an answer, a refusal, or the
 * bytes of a file. NULL is nothing. */
void fado_free(void *answer);

/* The version of the library, such as "v0.9.0", or "dev" for a build
 * that is no release. The library's own memory: never freed. */
const char *fado_version(void);

#ifdef __cplusplus
}
#endif

#endif /* FADODB_H */
