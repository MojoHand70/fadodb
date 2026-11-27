/*
 * The story of the chapter "In a program of another language", in C,
 * in the same process (docs/manual/other-languages.md). Run it in a
 * folder with invoice.json and nothing else:
 *
 *     cc -I bin -o books books.c -L bin -lfadodb-core
 *     ./books
 *
 * with fadodb.h and the library in bin, as make library leaves them;
 * the system finds the library when it runs as it finds any other.
 *
 * A real program reads the answers with a library for JSON; this one
 * finds the two members it prints by hand, so that it needs nothing.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "fadodb.h"

/* member copies the text of the first member "name":"…" of an answer
 * into out. */
static const char *member(const char *answer, const char *name, char *out, size_t size) {
    char key[64];
    snprintf(key, sizeof key, "\"%s\":\"", name);
    const char *at = strstr(answer, key);
    out[0] = '\0';
    if (at != NULL) {
        at += strlen(key);
        size_t n = strcspn(at, "\"");
        if (n >= size) {
            n = size - 1;
        }
        memcpy(out, at, n);
        out[n] = '\0';
    }
    return out;
}

/* run sends a command and answers its answer, or stops the program when
 * the library gave none. */
static char *run(fado_db db, const char *caller, const char *command, const char *object) {
    char *answer = fado_exec(db, caller, command, object);
    if (answer == NULL) {
        fprintf(stderr, "no answer to %s\n", command);
        exit(1);
    }
    return answer;
}

int main(void) {
    FILE *f = fopen("invoice.json", "rb");
    if (f == NULL) {
        fprintf(stderr, "no invoice.json here\n");
        return 1;
    }
    char invoice[4096];
    size_t n = fread(invoice, 1, sizeof invoice - 1, f);
    invoice[n] = '\0';
    fclose(f);

    char *refusal = NULL;
    fado_db db = fado_open("books.fado", NULL, &refusal);
    if (db == 0) {
        fprintf(stderr, "%s\n", refusal);
        fado_free(refusal);
        return 1;
    }
    const char *dan = "{\"id\": \"dan\", \"kind\": \"person\", \"system_groups\": [\"designers\"]}";
    const char *ann = "{\"id\": \"ann\", \"kind\": \"person\", \"groups\": [\"sales\"]}";
    const char *bob = "{\"id\": \"bob\", \"kind\": \"person\", \"groups\": [\"accounting\"]}";
    char object[8192], id[64], state[64], code[64];

    snprintf(object, sizeof object, "{\"definition\": %s}", invoice);
    char *answer = run(db, dan, "define_class", object);
    if (strncmp(answer, "{\"error\":", 9) == 0) {
        fprintf(stderr, "%s\n", answer);
        return 1;
    }
    fado_free(answer);
    printf("dan defines the class Invoice\n");

    answer = run(db, ann, "create",
                 "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"values\": {\"number\": \"FV/2026/11/0042\"}}");
    printf("ann makes %s: %s\n", member(answer, "id", id, sizeof id), member(answer, "state", state, sizeof state));
    fado_free(answer);

    answer = run(db, bob, "transition", "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"transition\": \"book\"}");
    if (strncmp(answer, "{\"error\":", 9) == 0) {
        printf("bob books it: refused, %s\n", member(answer, "code", code, sizeof code));
    }
    fado_free(answer);

    fado_free(run(db, bob, "set", "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"values\": {\"account\": \"400-01\"}}"));
    printf("bob gives the account 400-01\n");

    answer = run(db, bob, "transition", "{\"class\": \"Invoice\", \"id\": \"FV/2026/11/0042\", \"transition\": \"book\"}");
    printf("bob books it: %s\n", member(answer, "state", state, sizeof state));
    fado_free(answer);

    fado_close(db, NULL);
    return 0;
}
