/*
 * measure.c - what one command costs through the library of C, for the
 * page of v0.9.0 (docs/perf/v0.9.0.md). library_measure_test.go builds
 * it against the library and runs it:
 *
 *     measure FILE N ROUNDS
 *
 * It opens the database file, defines a note, makes one to read, and
 * then, ROUNDS times, reads it N times and makes N notes, printing the
 * microseconds of one read and of one make in each round, and once the
 * microseconds fado_open took.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "fadodb.h"

static double now(void) {
    struct timespec t;
    timespec_get(&t, TIME_UTC);
    return (double)t.tv_sec * 1e6 + (double)t.tv_nsec / 1e3;
}

static const char *dan = "{\"id\": \"dan\", \"kind\": \"person\", \"system_groups\": [\"designers\"]}";
static const char *ann = "{\"id\": \"ann\", \"kind\": \"person\"}";
static const char *note =
    "{\"definition\": {\"name\": \"Note\", \"variables\": {\"text\": {\"type\": \"text\"}},"
    "\"states\": {\"initial\": \"open\", \"list\": [\"open\"]},"
    "\"access\": {\"read\": \"TRUE\", \"create\": \"TRUE\", \"update\": \"TRUE\", \"delete\": \"FALSE\"}}}";

static void must(char *answer, const char *what) {
    if (answer == NULL || strncmp(answer, "{\"error\":", 9) == 0) {
        fprintf(stderr, "%s: %s\n", what, answer ? answer : "(no answer)");
        exit(1);
    }
    fado_free(answer);
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: measure FILE N ROUNDS\n");
        return 2;
    }
    int n = atoi(argv[2]), rounds = atoi(argv[3]);
    char *refusal = NULL;
    double began = now();
    fado_db db = fado_open(argv[1], NULL, &refusal);
    double opened = now() - began;
    if (db == 0) {
        fprintf(stderr, "%s\n", refusal);
        return 1;
    }
    printf("open %.1f\n", opened);
    must(fado_exec(db, dan, "define_class", note), "define_class");
    must(fado_exec(db, ann, "create", "{\"class\": \"Note\", \"id\": \"N-0\", \"values\": {\"text\": \"the one read\"}}"), "create");
    char object[160];
    int made = 0;
    for (int r = 0; r < rounds; r++) {
        began = now();
        for (int i = 0; i < n; i++) {
            fado_free(fado_exec(db, ann, "get", "{\"class\": \"Note\", \"id\": \"N-0\"}"));
        }
        double read = (now() - began) / n;
        began = now();
        for (int i = 0; i < n; i++) {
            snprintf(object, sizeof object, "{\"class\": \"Note\", \"id\": \"M-%d\", \"values\": {\"text\": \"a note\"}}", made++);
            fado_free(fado_exec(db, ann, "create", object));
        }
        double make = (now() - began) / n;
        printf("get %.2f\ncreate %.2f\n", read, make);
    }
    fado_close(db, NULL);
    return 0;
}
