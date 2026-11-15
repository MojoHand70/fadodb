package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// terminalChapter reads the chapter of the manual on commands from a
// terminal.
func terminalChapter(t *testing.T) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual", "commands-from-a-terminal.md"))
	if err != nil {
		t.Fatal(err)
	}
	// A checkout on Windows may carry CRLF line endings.
	return strings.ReplaceAll(string(text), "\r\n", "\n")
}

// typed are the arguments of a line of a terminal as a shell hands
// them to the tool: split at spaces, with what stands between single
// quotes kept whole. A line of the chapter begins with the name of the
// tool, and may send what it prints to a file.
func typed(t *testing.T, line string) (args []string, into string) {
	t.Helper()
	var word strings.Builder
	quoted, any := false, false
	end := func() {
		if any {
			args = append(args, word.String())
		}
		word.Reset()
		any = false
	}
	for _, r := range line {
		switch {
		case r == '\'':
			quoted, any = !quoted, true
		case r == ' ' && !quoted:
			end()
		default:
			word.WriteRune(r)
			any = true
		}
	}
	end()
	if quoted || len(args) == 0 || args[0] != "fadodb" {
		t.Fatalf("the chapter has a line the test cannot type: %s", line)
	}
	args = args[1:]
	if n := len(args); n >= 2 && args[n-2] == ">" {
		args, into = args[:n-2], args[n-1]
	}
	return args, into
}

var (
	shownMoments  = regexp.MustCompile(`"(created_at|updated_at|state_entered_at|ts)": ?\d+`)
	shownEventIDs = regexp.MustCompile(`"event_id":"[0-9A-Z]{26}"`)
	shownDays     = regexp.MustCompile(`"business_date":"\d{4}-\d\d-\d\d"`)
)

// asTheTerminalChapterShows puts the moments, the id of an event and
// the day of what the tool printed to the ones the chapter shows.
func asTheTerminalChapterShows(printed string) string {
	printed = shownMoments.ReplaceAllStringFunc(printed, func(m string) string {
		name, _, _ := strings.Cut(m, ":")
		if strings.Contains(m, ": ") {
			return name + ": 1794733200"
		}
		return name + ":1794733200"
	})
	printed = shownEventIDs.ReplaceAllString(printed, `"event_id":"01KA2F8Q7ZJ4M0X3T5V9B1C6DE"`)
	return shownDays.ReplaceAllString(printed, `"business_date":"2026-11-15"`)
}

// The chapter of the manual on commands from a terminal says what the
// tool does: every line of it is typed against a server, as the user
// who types it in the chapter, and what the tool prints is compared.
func TestTheChapterOnTheTerminal(t *testing.T) {
	chapter := terminalChapter(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "acme.fado")
	for _, args := range [][]string{
		{"init", path},
		{"user", "add", "olga", "--kind", "person", "--system", "administrators", "--system", "designers", "--db", path},
	} {
		if code, _, said := tool(args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, said)
		}
	}
	_, olga, _ := tool("key", "new", "olga", "--label", "laptop", "--db", path)
	keys := map[string]string{"olga": strings.TrimSpace(olga)}
	addr, _, stop := serving(t, path, "--listen", "127.0.0.1:0")
	t.Setenv("FADODB_URL", "http://"+addr)

	// The files of the chapter are beside the terminal.
	for _, file := range []string{"invoice.json", "approve-0043.json"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(blockOf(t, chapter, file)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	scan := []byte("%PDF-1.4 the scan of an invoice\n")
	if err := os.WriteFile(filepath.Join(dir, "scan-0045.pdf"), scan, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	// play types the lines of a block as the user its mark names, and
	// answers how the last ended and what each printed.
	play := func(mark string) (code int, out, said []string) {
		t.Helper()
		user, _, _ := strings.Cut(mark, ":")
		if keys[user] == "" {
			t.Fatalf("%s has no key yet", user)
		}
		t.Setenv("FADODB_KEY", keys[user])
		for _, line := range strings.Split(blockOf(t, chapter, mark), "\n") {
			args, _ := typed(t, line)
			var o, s string
			code, o, s = tool(args...)
			out, said = append(out, o), append(said, s)
		}
		return code, out, said
	}
	// done types a block whose every line must be carried out.
	done := func(mark string) []string {
		t.Helper()
		code, out, said := play(mark)
		for i := range said {
			if said[i] != "" && !strings.Contains(said[i], "taken") {
				t.Fatalf("%s, line %d said: %s", mark, i+1, said[i])
			}
		}
		if code != exitDone {
			t.Fatalf("%s: exit %d", mark, code)
		}
		return out
	}

	// Olga checks the class in its file and defines it, then makes Bob
	// and Ann, and a key for each.
	done("olga: the class")
	made := done("olga: the users")
	for i, user := range []string{"bob", "ann"} {
		var answer struct {
			Key struct{ Secret string }
		}
		if err := json.Unmarshal([]byte(made[2+i]), &answer); err != nil || !strings.HasPrefix(answer.Key.Secret, "fado_") {
			t.Fatalf("the key of %s: %v in %s", user, err, made[2+i])
		}
		keys[user] = answer.Key.Secret
	}

	// Bob files three invoices and submits them; Ann approves one.
	done("bob: three invoices")
	approved := done("ann: approve")
	if got, want := asTheTerminalChapterShows(approved[0]), blockOf(t, chapter, "ann: approve, the answer")+"\n"; got != want {
		t.Errorf("the answer of the approval is\n%s\nand the chapter shows\n%s", got, want)
	}

	// Bob is refused: to standard error, nothing to standard output,
	// and 1.
	code, out, said := play("bob: approve")
	if code != exitRefused || out[0] != "" {
		t.Errorf("Bob approves: exit %d, printed %q", code, out[0])
	}
	if want := blockOf(t, chapter, "bob: approve, the refusal") + "\n"; said[0] != want {
		t.Errorf("the refusal is\n%s\nand the chapter shows\n%s", said[0], want)
	}

	// The object from a file, and the export: an Excel file whose sheet
	// is the table of the chapter, with the totals kept as numbers.
	done("ann: approve from a file")
	args, _ := typed(t, blockOf(t, chapter, "ann: the export"))
	t.Setenv("FADODB_KEY", keys["ann"])
	if code, out, said := tool(args...); code != exitDone || out != "" || said != "" {
		t.Fatalf("the export: exit %d, printed %q, said %q", code, out, said)
	}
	written, err := os.ReadFile("approved.xlsx")
	if err != nil {
		t.Fatalf("the export of the chapter wrote no approved.xlsx: %v", err)
	}
	name, cells := readSheet(t, written)
	if name != "Invoice" {
		t.Errorf("the sheet of the export is named %q", name)
	}
	_, table, _ := strings.Cut(chapter, "<!-- ann: the export, the sheet -->\n")
	table, _, _ = strings.Cut(table, "\n\n")
	for r, line := range strings.Split(table, "\n") {
		if r == 1 {
			continue // the line under the names
		}
		row := r + 1
		if r > 1 {
			row = r
		}
		for c, shown := range strings.Split(strings.Trim(line, "|"), "|") {
			shown = strings.TrimSpace(shown)
			want := "text: " + shown
			switch {
			case r == 0:
				want = "name: " + shown
			case c == 3:
				want = "number: " + shown + " shown as 0.00"
			}
			if at := columnName(c) + strconv.Itoa(row); cells[at] != want {
				t.Errorf("%s of the export holds %q, and the chapter shows %q", at, cells[at], want)
			}
		}
	}
	if len(cells) != 12 {
		t.Errorf("the export has %d cells, and the chapter shows 12", len(cells))
	}
	// A cell, as the table of the chapter says, by the type of its
	// variable.
	inTicks := regexp.MustCompile("^`([^`]*)`")
	for _, row := range rowsOf(t, chapter, "a cell of an Excel file") {
		declared, value := "", inTicks.FindStringSubmatch(row[1])
		if named := inTicks.FindStringSubmatch(row[0]); named != nil {
			declared = named[1]
		} else if !strings.HasPrefix(row[0], "computed") {
			t.Fatalf("the table of the cells has a row for %q", row[0])
		}
		if value == nil {
			t.Fatalf("the table of the cells has a row %q", row)
		}
		got := cellOf(declared, []byte(value[1]))
		holds := strings.Trim(row[2], "`")
		kind, said := map[cellKind]string{cellText: "the text ", cellNumber: "the number ", cellDay: "the day ", cellMoment: "the moment ", cellBool: "true"}[got.kind], got.text
		if got.kind == cellDay || got.kind == cellMoment || got.kind == cellBool {
			said = ""
		}
		if !strings.HasPrefix(strings.ReplaceAll(holds, "`", ""), kind+said) {
			t.Errorf("a %s value %s is the cell %+v, and the chapter says: %s", row[0], value[1], got, row[2])
		}
		if got.unit != "" && !strings.Contains(holds, got.unit) {
			t.Errorf("a %s value %s has the currency %s, and the chapter says: %s", row[0], value[1], got.unit, row[2])
		}
	}
	// The same as text, for a program.
	args, _ = typed(t, blockOf(t, chapter, "ann: the export as text"))
	code, file, complaint := tool(args...)
	if want := blockOf(t, chapter, "ann: the export as text, printed") + "\n"; code != exitDone || file != want {
		t.Errorf("the export as text: exit %d, said %q, printed\n%s\nand the chapter shows\n%s", code, complaint, file, want)
	}

	// Ann waits for invoices that enter review, Bob files one more, and
	// her terminal prints a line.
	args, _ = typed(t, blockOf(t, chapter, "ann: wait"))
	ctx, interrupt := context.WithCancel(context.Background())
	defer interrupt()
	var printed, told buffer
	ended := make(chan int, 1)
	go func() { ended <- runExec(ctx, args[1:], &printed, &told) }()
	// The tool says when the stream is open, in the words of the
	// chapter, and Bob waits for them: the server counts a stream
	// before it has opened it, so the count is no sign of it.
	const isOpen = "fadodb exec: the stream is open"
	if !strings.Contains(chapter, "`"+isOpen+"`") {
		t.Errorf("the chapter does not say what the tool says when a stream is open: %s", isOpen)
	}
	for start := time.Now(); told.String() != isOpen+"\n"; time.Sleep(5 * time.Millisecond) {
		if time.Since(start) > 10*time.Second {
			t.Fatalf("Ann's stream was not opened; the tool said %q", told.String())
		}
	}
	done("bob: one more")
	for start := time.Now(); !strings.HasSuffix(printed.String(), "\n"); time.Sleep(5 * time.Millisecond) {
		if time.Since(start) > 10*time.Second {
			t.Fatalf("Ann's terminal printed %q and said %q", printed.String(), told.String())
		}
	}
	if got, want := asTheTerminalChapterShows(printed.String()), blockOf(t, chapter, "ann: wait, a line")+"\n"; got != want {
		t.Errorf("Ann's terminal printed\n%s\nand the chapter shows\n%s", got, want)
	}
	interrupt()
	select {
	case code := <-ended:
		if code != exitDone || told.String() != isOpen+"\n" {
			t.Errorf("a stream its caller closes: exit %d, said %q", code, told.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the tool did not end when it was interrupted")
	}
	// The table of how a stream ends names the three ends, which
	// TestExecFollowsAStream plays.
	ends := rowsOf(t, chapter, "how a stream ends")
	if len(ends) != 3 || ends[0][1] != strconv.Itoa(exitDone) || ends[1][1] != strconv.Itoa(exitRefused) || ends[2][1] != strconv.Itoa(exitNoAnswer) {
		t.Errorf("the chapter says a stream ends with %v", ends)
	}

	// A file on a document: Bob puts the scan, Ann reads the same bytes.
	done("bob: the scan")
	done("ann: the scan")
	if read, err := os.ReadFile("scan.pdf"); err != nil || string(read) != string(scan) {
		t.Errorf("Ann read %q of the scan (%v)", read, err)
	}

	// The help of a command.
	t.Setenv("FADODB_KEY", "")
	if _, help, _ := tool("exec", "help", "transition"); help != blockOf(t, chapter, "the help of a command")+"\n" {
		t.Errorf("the help of transition is\n%s\nand the chapter shows\n%s", help, blockOf(t, chapter, "the help of a command"))
	}

	// What the tool is told: each flag and each variable of the
	// chapter's table is one the tool reads.
	for _, row := range rowsOf(t, chapter, "what the tool is told") {
		flag, _, _ := strings.Cut(strings.TrimPrefix(row[1], "`"), "`")
		if !strings.Contains(execUsage, "\n  "+flag+" ") {
			t.Errorf("the chapter names the flag %s, which the tool does not take", flag)
		}
		if env := strings.Trim(row[2], "`"); env != "" && !strings.Contains(execUsage, env) {
			t.Errorf("the chapter names %s, which the tool does not read", env)
		}
	}
	if strings.Count(execUsage, "\n  --") != len(rowsOf(t, chapter, "what the tool is told"))+4 {
		t.Error("the tool has a flag the chapter does not name: its table has the four of the server and the key, and the chapter --from, --xlsx, --csv and --out")
	}
	for _, flag := range []string{"--from", "--xlsx", "--csv", "--out"} {
		if !strings.Contains(chapter, "`"+flag) || !strings.Contains(execUsage, "\n  "+flag+" ") {
			t.Errorf("the flag %s is not in the chapter and in the help of the tool", flag)
		}
	}

	// How the tool ends: the four ends of the chapter's table, each as
	// it happens. The first two happened above.
	rows := rowsOf(t, chapter, "how the tool ends")
	if len(rows) != 4 {
		t.Fatalf("the chapter says the tool ends in %d ways", len(rows))
	}
	for i, want := range []int{exitDone, exitRefused, exitUsage, exitNoAnswer} {
		if rows[i][0] != strconv.Itoa(want) {
			t.Errorf("row %d of how the tool ends is for %s, want %d", i+1, rows[i][0], want)
		}
	}
	// With no key the line cannot be read, and nothing is sent.
	if code, _, _ := tool("exec", "list_classes"); code != exitUsage {
		t.Errorf("with no key the tool ends with %d", code)
	}
	// With no server, no answer comes.
	if code := stop(); code != 0 {
		t.Errorf("the server ended with %d", code)
	}
	t.Setenv("FADODB_KEY", keys["ann"])
	if code, _, said := tool("exec", "list_classes"); code != exitNoAnswer {
		t.Errorf("with no server the tool ends with %d and says %q", code, said)
	}
}
