package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter "Running it" of the manual (docs/manual/running-it.md)
// shows what "fadodb check" prints and says how it ends.
// TestTheChapterOnRunning makes the database of the chapter, runs the
// command as the chapter does, and holds both to the tool.
func TestTheChapterOnRunning(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual", "running-it.md"))
	if err != nil {
		t.Fatal(err)
	}
	// A checkout on Windows may carry CRLF line endings.
	chapter := strings.ReplaceAll(string(data), "\r\n", "\n")
	// fenced is the block under a marker of the chapter.
	fenced := func(marker string) string {
		t.Helper()
		_, rest, found := strings.Cut(chapter, "<!-- "+marker+" -->\n```text\n")
		if !found {
			t.Fatalf("the chapter has no block under the marker %q", marker)
		}
		body, _, _ := strings.Cut(rest, "```")
		return body
	}
	// The action and the class, as the chapter shows them.
	var mail, receipt string
	for _, m := range regexp.MustCompile("(?s)```json\n(.*?)\n```").FindAllStringSubmatch(chapter, -1) {
		switch {
		case strings.Contains(m[1], `"variables"`):
			receipt = m[1]
		case strings.Contains(m[1], `"params"`):
			mail = m[1]
		}
	}
	if mail == "" || receipt == "" {
		t.Fatal("the chapter shows the action mail.send and the class Receipt")
	}

	// The command is run where the database is, as the chapter runs it.
	t.Chdir(t.TempDir())
	ctx := context.Background()
	designer := fado.As(fado.Principal{ID: "dan", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}})
	ola := fado.As(fado.Principal{ID: "ola", Kind: fado.ActorPerson, Groups: []string{"sales"}})
	program := fado.Options{
		BlobStore: "dir",
		Outbox:    fado.OutboxOptions{Manual: true},
		Actions: map[string]fado.ActionFunc{
			"mail.send": func(context.Context, *fado.ActionCall) (fado.Values, error) { return nil, nil },
		},
	}
	db, err := fado.Open("books.fado", program)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineAction(ctx, []byte(mail), designer); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineClass(ctx, []byte(receipt), designer); err != nil {
		t.Fatal(err)
	}
	var hashes []string
	for _, id := range []string{"R-1", "R-2"} {
		scan, err := db.PutBlob(ctx, "scan-"+id+".txt", "text/plain", strings.NewReader("the scan of "+id), ola)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Create(ctx, "Receipt", fado.Values{"what": "a taxi", "scan": scan}, ola, fado.WithID(id)); err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, fmt.Sprint(scan["hash"]))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	check := func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := run(append([]string{"check"}, args...), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
	ends := map[string]int{}

	code, out, said := check("books.fado")
	if out != fenced("the check") {
		t.Errorf("the check prints:\n%s%s\nand the chapter shows:\n%s", out, said, fenced("the check"))
	}
	ends["finds every file as it was put"] = code

	// Adam changes one byte of the scan of R-1 on disk.
	scan := filepath.Join("books.fado.blobs", hashes[0][:2], hashes[0])
	bytesOf, err := os.ReadFile(scan)
	if err != nil {
		t.Fatal(err)
	}
	bytesOf[0] ^= 1
	if err := os.WriteFile(scan, bytesOf, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, said = check("books.fado")
	if out != fenced("the check, a file altered") {
		t.Errorf("the check of an altered file prints:\n%s%s\nand the chapter shows:\n%s", out, said, fenced("the check, a file altered"))
	}
	ends["names a file"] = code

	// And the scan of R-2 is removed from the disk.
	if err := os.Remove(filepath.Join("books.fado.blobs", hashes[1][:2], hashes[1])); err != nil {
		t.Fatal(err)
	}
	code, out, said = check("books.fado")
	if out != fenced("the check, a file missing") || code != 1 {
		t.Errorf("the check of a database that lost a file ends with %d and prints:\n%s%s\nand the chapter shows:\n%s", code, out, said, fenced("the check, a file missing"))
	}

	// Another program holds the database alone.
	held, err := fado.Open("books.fado", fado.Options{Alone: "the program of Acme", Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	code, out, said = check("books.fado")
	held.Close()
	if !strings.Contains(said, "in_use") || out != "" {
		t.Errorf("the check of a database another program holds alone says %q and prints %q", said, out)
	}
	ends["cannot open the database, because another program holds it alone"] = code

	code, _, _ = check()
	ends["is called with no database"] = code

	// How the check ends, as the chapter's table says.
	_, table, found := strings.Cut(chapter, "<!-- how the check ends -->\n")
	if !found {
		t.Fatal("the chapter has no table of how the check ends")
	}
	table, _, _ = strings.Cut(table, "\n\n")
	listed := 0
	for i, line := range strings.Split(table, "\n") {
		if i < 2 {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		what, with := strings.TrimSpace(cells[0]), strings.TrimSpace(cells[1])
		listed++
		if code, ok := ends[what]; !ok {
			t.Errorf("the chapter has the row %q, and the test does not play it", what)
		} else if fmt.Sprint(code) != with {
			t.Errorf("the check that %s ends with %d, and the chapter says %s", what, code, with)
		}
	}
	if listed != len(ends) {
		t.Errorf("the chapter lists %d ways the check ends, and the test plays %d", listed, len(ends))
	}
}
