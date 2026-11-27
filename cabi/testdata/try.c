/*
 * try.c - a program of C that uses FadoDB through fadodb.h, as any
 * program that loads the library does (ADR-0101). library_c_test.go
 * builds the library, compiles this against it and runs it:
 *
 *     try books.fado
 *
 * Ann of sales makes an invoice, Bob of accounting gives its account,
 * the ledger agent may not book it and Bob does; then sixteen threads
 * make invoices at once. It prints what it did and ends with 0, or says
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
