/*
 * try.c - a program of C that uses FadoDB through fadodb.h, as any
 * program that loads the library does (ADR-0101). library_c_test.go
 * builds the library, compiles this against it and runs it:
 *
 *     try books.fado
 *
 * Ann of sales makes an invoice, Bob of accounting gives its account,
 * the ledger agent may not book it and Bob does; then sixteen threads
 * make invoices at once; then Ann puts the scan of a taxi receipt on a
 * claim and Bob reads it back. It prints what it did and ends with 0, or says
 * what was not as it should be and ends with 1.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "fadodb.h"

#ifdef _WIN32
#include <windows.h>
#else
#include <pthread.h>
#endif

static const char *invoice =
    "{\"definition\": {"
    "\"name\": \"Invoice\","
    "\"variables\": {\"number\": {\"type\": \"text\", \"required\": true}, \"account\": {\"type\": \"text\"}},"
    "\"states\": {\"initial\": \"open\", \"list\": [\"open\", \"booked\"], \"final\": [\"booked\"]},"
    "\"transitions\": [{\"name\": \"book\", \"from\": \"open\", \"to\": \"booked\", \"guard\": \"NOT ISBLANK(account)\"}],"
    "\"access\": {\"read\": \"CREATEDBY() = USER() OR INGROUP(\\\"accounting\\\")\","
    "\"create\": \"INGROUP(\\\"sales\\\")\", \"update\": \"INGROUP(\\\"accounting\\\")\", \"delete\": \"FALSE\","
    "\"transition\": {\"book\": \"INGROUP(\\\"accounting\\\") AND USERKIND() = \\\"person\\\"\"}}}}";

static const char *claim =
    "{\"definition\": {"
    "\"name\": \"Claim\","
    "\"variables\": {\"number\": {\"type\": \"text\", \"required\": true}, \"scan\": {\"type\": \"object\", \"schema\": \"File\"}},"
    "\"states\": {\"initial\": \"open\", \"list\": [\"open\"]},"
    "\"access\": {\"read\": \"CREATEDBY() = USER() OR INGROUP(\\\"accounting\\\")\","
    "\"create\": \"INGROUP(\\\"sales\\\")\", \"update\": \"CREATEDBY() = USER()\", \"delete\": \"FALSE\"}}}";

static const char *dan = "{\"id\": \"dan\", \"kind\": \"person\", \"system_groups\": [\"designers\"]}";
static const char *ann = "{\"id\": \"ann\", \"kind\": \"person\", \"groups\": [\"sales\"]}";
static const char *bob = "{\"id\": \"bob\", \"kind\": \"person\", \"groups\": [\"accounting\"]}";
static const char *robot = "{\"id\": \"ledger-bot\", \"kind\": \"agent\", \"groups\": [\"accounting\"]}";

static int failures = 0;

/* expect runs a command and holds its answer to hold the text want. */
static void expect(fado_db db, const char *caller, const char *command, const char *object, const char *want) {
    char *answer = fado_exec(db, caller, command, object);
    if (answer == NULL) {
        printf("FAIL %s: no answer\n", command);
        failures++;
        return;
    }
    if (strstr(answer, want) == NULL) {
        printf("FAIL %s: the answer holds no %s:\n  %s\n", command, want, answer);
        failures++;
    } else {
        printf("%s: %s\n", command, want);
    }
    fado_free(answer);
}

#define THREADS 16
#define EACH 20

struct work {
    fado_db db;
    int thread;
    int made;
};

#ifdef _WIN32
static DWORD WINAPI make(LPVOID arg)
#else
static void *make(void *arg)
#endif
{
    struct work *w = arg;
    char object[160];
    for (int i = 0; i < EACH; i++) {
        snprintf(object, sizeof object,
                 "{\"class\": \"Invoice\", \"id\": \"T%d-%d\", \"values\": {\"number\": \"T%d-%d\"}}",
                 w->thread, i, w->thread, i);
        char *answer = fado_exec(w->db, ann, "create", object);
        if (answer != NULL && strncmp(answer, "{\"document\":", 12) == 0) {
            w->made++;
        }
        fado_free(answer);
    }
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 2) {
        fprintf(stderr, "usage: try FILE\n");
        return 2;
    }
    printf("library %s\n", fado_version());

    char *refusal = NULL;
    fado_db bad = fado_open(argv[1], "{\"time_zone\": \"UTC\"}", &refusal);
    if (bad != 0 || refusal == NULL || strstr(refusal, "\"path\":\"/time_zone\"") == NULL) {
        printf("FAIL options no database takes were not refused: %s %s\n", bad != 0 ? "opened" : "not opened", refusal ? refusal : "(none)");
        failures++;
    } else {
        printf("fado_open: refused /time_zone\n");
    }
    fado_free(refusal);

    fado_db db = fado_open(argv[1], "{\"timezone\": \"Europe/Warsaw\"}", &refusal);
    if (db == 0) {
        printf("FAIL fado_open: %s\n", refusal);
        fado_free(refusal);
        return 1;
    }

    expect(db, dan, "define_class", invoice, "\"class\":");
    expect(db, ann, "create", "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"values\": {\"number\": \"FV/2026/11/0042\"}}", "\"state\":\"open\"");
    expect(db, bob, "set", "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"values\": {\"account\": \"400-01\"}}", "\"account\":\"400-01\"");
    expect(db, robot, "transition", "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"transition\": \"book\"}", "\"code\":\"forbidden\"");
    expect(db, bob, "transition", "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"transition\": \"book\"}", "\"state\":\"booked\"");
    expect(db, "{\"id\": 7}", "get", "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\"}", "\"path\":\"/id\"");
    expect(db, NULL, "create", NULL, "\"code\":\"bad_json\"");

    struct work works[THREADS];
#ifdef _WIN32
    HANDLE threads[THREADS];
#else
    pthread_t threads[THREADS];
#endif
    for (int t = 0; t < THREADS; t++) {
        works[t].db = db;
        works[t].thread = t;
        works[t].made = 0;
#ifdef _WIN32
        threads[t] = CreateThread(NULL, 0, make, &works[t], 0, NULL);
#else
        pthread_create(&threads[t], NULL, make, &works[t]);
#endif
    }
    int made = 0;
    for (int t = 0; t < THREADS; t++) {
#ifdef _WIN32
        WaitForSingleObject(threads[t], INFINITE);
        CloseHandle(threads[t]);
#else
        pthread_join(threads[t], NULL);
#endif
        made += works[t].made;
    }
    if (made != THREADS * EACH) {
        printf("FAIL %d threads made %d invoices of %d\n", THREADS, made, THREADS * EACH);
        failures++;
    } else {
        printf("%d threads: %d invoices\n", THREADS, made);
    }
    expect(db, bob, "count", "{\"class\": \"Invoice\"}", "\"count\":321");

    /* A file: its bytes travel beside the commands, its facts in them. */
    expect(db, dan, "define_class", claim, "\"class\":");
    expect(db, ann, "create", "{\"class\": \"Claim\", \"id\": \"C-7\", \"values\": {\"number\": \"C-7\"}}", "\"state\":\"open\"");
    static const char scan[] = "%PDF-1.7 the taxi from the airport, 84.00 PLN\0and a zero byte in it";
    char *put = fado_put_file(db, ann, "{\"name\": \"taxi.pdf\", \"media_type\": \"application/pdf\"}", scan, sizeof scan);
    const char *at = put ? strstr(put, "\"file\":") : NULL;
    if (at == NULL) {
        printf("FAIL fado_put_file: %s\n", put ? put : "(no answer)");
        failures++;
    } else {
        /* {"file":{…}} is the facts to commit; the hash is in them. */
        char set[512], get[256];
        size_t facts = strlen(at + 7) - 1;
        snprintf(set, sizeof set, "{\"class\": \"Claim\", \"id\": \"C-7\", \"values\": {\"scan\": %.*s}}", (int)facts, at + 7);
        expect(db, ann, "set", set, "\"name\":\"taxi.pdf\"");
        const char *hash = strstr(put, "\"hash\":\"");
        snprintf(get, sizeof get, "{\"class\": \"Claim\", \"id\": \"C-7\", \"hash\": \"%.64s\"}", hash ? hash + 8 : "");
        void *bytes = NULL;
        size_t length = 0;
        char *got = fado_get_file(db, bob, get, &bytes, &length);
        if (bytes == NULL || length != sizeof scan || memcmp(bytes, scan, sizeof scan) != 0) {
            printf("FAIL fado_get_file gave %d bytes: %s\n", (int)length, got);
            failures++;
        } else {
            printf("fado_get_file: the %d bytes Ann put\n", (int)length);
        }
        fado_free(got);
        fado_free(bytes);
        got = fado_get_file(db, robot, "{\"class\": \"Claim\", \"id\": \"C-7\"}", &bytes, &length);
        if (bytes != NULL || got == NULL || strstr(got, "\"path\":\"/hash\"") == NULL) {
            printf("FAIL a get with no hash: %s\n", got ? got : "(no answer)");
            failures++;
        } else {
            printf("fado_get_file: refused /hash\n");
        }
        fado_free(got);
    }
    fado_free(put);

    if (fado_close(db, &refusal) != 0) {
        printf("FAIL fado_close: %s\n", refusal);
        fado_free(refusal);
        failures++;
    }
    expect(db, bob, "count", "{\"class\": \"Invoice\"}", "\"code\":\"closed\"");
    if (fado_close(db, NULL) != -1) {
        printf("FAIL a second fado_close was not refused\n");
        failures++;
    }

    if (failures > 0) {
        printf("%d failures\n", failures);
        return 1;
    }
    printf("ok\n");
    return 0;
}
