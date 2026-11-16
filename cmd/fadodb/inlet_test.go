package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/fado/httpdoor"
	"github.com/MojoHand70/fadodb/internal/model"
)

// The chapter of the manual on the way in for messages (ADR-0065),
// docs/manual/a-way-in-for-messages.md, played against a server: the
// settings that turn the way in on, the check of the inlet from the
// command line, the bank's notice with its signature, every answer a
// sender reads, the lines the server writes and its counts.

// wayIn is a block of the chapter, by the mark that stands before it.
func wayIn(t *testing.T, mark string) string {
	t.Helper()
	chapter := fromTheManual(t, "a-way-in-for-messages.md")
	_, after, found := strings.Cut(chapter, "<!-- "+mark+" -->\n```")
	if !found {
		t.Fatalf("the chapter has no block marked %q", mark)
	}
	_, after, _ = strings.Cut(after, "\n")
	block, _, found := strings.Cut(after, "\n```")
	if !found {
		t.Fatalf("the block marked %q does not end", mark)
	}
	return block
}

// wayInRows are the rows of a table of the chapter, by its mark, each
// with its cells.
func wayInRows(t *testing.T, mark string) [][]string {
	t.Helper()
	chapter := fromTheManual(t, "a-way-in-for-messages.md")
	_, after, found := strings.Cut(chapter, "<!-- "+mark+" -->\n")
	if !found {
		t.Fatalf("the chapter has no table marked %q", mark)
	}
	table, _, _ := strings.Cut(after, "\n\n")
	var rows [][]string
	for i, line := range strings.Split(table, "\n") {
		if i < 2 {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		rows = append(rows, cells)
	}
	return rows
}

// fromTheManual reads a chapter of the manual.
func fromTheManual(t *testing.T, name string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual", name))
	if err != nil {
		t.Fatal(err)
	}
	// A checkout on Windows may carry CRLF line endings.
	return strings.ReplaceAll(string(text), "\r\n", "\n")
}

// secretOfTheBank is the secret Acme and its bank share in the test.
const secretOfTheBank = "whsec_acme_and_its_bank"

// notice sends a message to an inlet of a server as the bank does: no
// key, the body as it is, signed with the secret at this moment. It
// answers the status and the body.
func notice(t *testing.T, base, inlet, secret, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/v1/in/"+inlet, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	at := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Bank-Signature", "t="+at+",v1="+fado.Sign(secret, at, []byte(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	text, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(text))
}

var (
	shownTime = regexp.MustCompile(`"time":"[^"]*"`)
	shownMs   = regexp.MustCompile(`"ms":[0-9.]+`)
)

// asShown puts the time and the duration of a line of the server to
// what a comparison leaves out.
func asShown(line string) string {
	return shownMs.ReplaceAllString(shownTime.ReplaceAllString(line, `"time":"…"`), `"ms":…`)
}

func TestTheChapterOnTheWayIn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "acme.fado")
	files := map[string]string{
		"invoice.json":   wayIn(t, "the class"),
		"bank-paid.json": wayIn(t, "the inlet"),
		"notice.json":    wayIn(t, "the sample"),
		"settings.json":  wayIn(t, "the settings"),
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		files[name] = filepath.Join(dir, name)
	}
	// The secret is Tomek's, beside the server: the file the chapter
	// names, in the directory of secrets.
	if !strings.Contains(fromTheManual(t, "a-way-in-for-messages.md"), "the file `bank/signing_key`") {
		t.Error("the chapter does not say where the secret is")
	}
	secrets := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(filepath.Join(secrets, "bank"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "bank", "signing_key"), []byte(secretOfTheBank+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The database, with Olga, who keeps it and is in finance, Dan, who
	// designs, and Ann, who does neither.
	if code, _, said := tool("init", path); code != 0 {
		t.Fatalf("init: %s", said)
	}
	keys := map[string]string{}
	for id, flags := range map[string][]string{
		"olga": {"--kind", "person", "--system", "administrators", "--system", "designers", "--group", "finance"},
		"dan":  {"--kind", "person", "--system", "designers"},
		"ann":  {"--kind", "person", "--group", "sales"},
	} {
		if code, _, said := tool(append([]string{"user", "add", id, "--db", path}, flags...)...); code != 0 {
			t.Fatalf("user add %s: %s", id, said)
		}
		code, key, said := tool("key", "new", id, "--db", path)
		if code != 0 {
			t.Fatalf("key new %s: %s", id, said)
		}
		keys[id] = strings.TrimSpace(key)
	}
	served := []string{path, "--listen", "127.0.0.1:0", "--settings", files["settings.json"], "--secrets-dir", secrets}
	addr, log, stop := serving(t, served...)
	base := "http://" + addr
	t.Setenv("FADODB_URL", base)
	// as types a line of the chapter as the user who types it there,
	// with the files of the chapter where the test wrote them.
	as := func(user, line string) (int, string, string) {
		t.Helper()
		t.Setenv("FADODB_KEY", keys[user])
		args, _ := typed(t, line)
		for i, arg := range args {
			if at, known := files[arg]; known {
				args[i] = at
			}
			if member, file, ok := strings.Cut(arg, "="); ok && files[file] != "" {
				args[i] = member + "=" + files[file]
			}
		}
		return tool(args...)
	}
	send := func(user, name, object string) (int, string) {
		t.Helper()
		return ask(t, http.DefaultClient, http.MethodPost, base+"/v1/"+name, keys[user], object)
	}
	must := func(user, name, object string) string {
		t.Helper()
		status, body := send(user, name, object)
		if status != http.StatusOK {
			t.Fatalf("%s %s: %d %s", name, object, status, body)
		}
		return body
	}
	must("olga", "define_class", `{"definition": `+wayIn(t, "the class")+`}`)
	for _, id := range []string{"FV/2026/11/0043", "FV/2026/11/0044"} {
		must("olga", "create", `{"class": "Invoice", "id": "`+id+`", "values": {"customer": "Globex", "total": "2400.00 PLN"}}`)
	}

	// Olga makes the user the inlet acts as.
	if code, _, said := as("olga", wayIn(t, "olga: the user")); code != 0 {
		t.Fatalf("the user of the inlet: %s", said)
	}
	// Dan checks the inlet in a file, with the class and a sample: the
	// check needs no server, and prints what the chapter shows.
	code, out, said := as("dan", wayIn(t, "dan: the check"))
	if want := wayIn(t, "dan: the check, the answer") + "\n"; code != 0 || said != "" ||
		strings.ReplaceAll(out, files["bank-paid.json"], "bank-paid.json") != strings.ReplaceAll(want, "notice.json", files["notice.json"]) {
		t.Errorf("the check of the inlet: exit %d\n%s%s\nwant\n%s", code, out, said, want)
	}
	// An inlet is an administrator's to define: Dan is refused, and
	// Olga defines it.
	if code, _, said := as("dan", wayIn(t, "olga: the inlet")); code != 1 || !strings.Contains(said, `"forbidden"`) {
		t.Errorf("the inlet defined by a designer: exit %d\n%s", code, said)
	}
	if code, out, said := as("olga", wayIn(t, "olga: the inlet")); code != 0 || !strings.Contains(out, `"name": "bank.paid"`) {
		t.Fatalf("the inlet defined by Olga: exit %d\n%s%s", code, out, said)
	}

	// The bank sends its notice, as the chapter writes it: to the
	// address of the inlet, signed in the header the inlet names.
	lines := strings.Split(wayIn(t, "the message"), "\n")
	if len(lines) != 5 || lines[0] != "POST https://fado.acme.example/v1/in/bank.paid" || lines[1] != "Content-Type: application/json" ||
		!strings.HasPrefix(lines[2], "Bank-Signature: t=") || !strings.Contains(lines[2], ",v1=") || lines[3] != "" {
		t.Fatalf("the message of the chapter:\n%s", strings.Join(lines, "\n"))
	}
	paid := lines[4]
	other := strings.Replace(paid, "0043", "0044", 1)
	status, body := notice(t, base, "bank.paid", secretOfTheBank, paid)
	if status != http.StatusOK || body != wayIn(t, "the answer") {
		t.Fatalf("the notice of the bank: %d %s", status, body)
	}
	// The invoice is paid, by bank-in, with the day and the bank's
	// number.
	invoice := func(id string) map[string]any {
		t.Helper()
		var got struct{ Document map[string]any }
		if err := json.Unmarshal([]byte(must("olga", "get", `{"class": "Invoice", "id": "`+id+`"}`)), &got); err != nil {
			t.Fatal(err)
		}
		return got.Document
	}
	var shown map[string]any
	if err := json.Unmarshal([]byte(wayIn(t, "the invoice")), &shown); err != nil {
		t.Fatal(err)
	}
	got := invoice("FV/2026/11/0043")
	for member, want := range shown {
		if a, b := mustJSONText(t, got[member]), mustJSONText(t, want); a != b {
			t.Errorf("the invoice has %s %s, and the chapter shows %s", member, a, b)
		}
	}

	// Twice is once: the notice again is answered as the first was, and
	// the invoice is at the version it had.
	if status, body := notice(t, base, "bank.paid", secretOfTheBank, paid); status != http.StatusOK || body != `{"ok":true}` || invoice("FV/2026/11/0043")["version"] != float64(2) {
		t.Errorf("the notice again: %d %s, the invoice at version %v", status, body, invoice("FV/2026/11/0043")["version"])
	}

	// What the sender is answered: a row of the chapter's table for
	// each, and nothing of the database in any.
	answered := wayInRows(t, "what the sender is answered")
	row := func(status int, body string) {
		t.Helper()
		for _, cells := range answered {
			if len(cells) == 3 && cells[1] == strconv.Itoa(status) && cells[2] == "`"+body+"`" {
				return
			}
		}
		t.Errorf("the chapter's table has no row for %d %s", status, body)
	}
	row(http.StatusOK, `{"ok":true}`)
	sent := func(what string, wantStatus int, inlet, secret, message string) {
		t.Helper()
		status, body := notice(t, base, inlet, secret, message)
		if status != wantStatus {
			t.Errorf("%s: %d %s, want %d", what, status, body, wantStatus)
		}
		row(status, body)
		for _, word := range []string{"Globex", "INGROUP", "FV/2026", "bank-in", `"message"`, `"hint"`} {
			if strings.Contains(body, word) {
				t.Errorf("%s: the sender is told %q: %s", what, word, body)
			}
		}
	}
	sent("a proof that does not hold", http.StatusUnauthorized, "bank.paid", "another secret", other)
	sent("a message of another kind", http.StatusOK, "bank.paid", secretOfTheBank, strings.Replace(other, "payment.received", "payment.returned", 1))
	sent("an invoice that is paid already", http.StatusConflict, "bank.paid", secretOfTheBank, strings.Replace(paid, "ntc_8841", "ntc_8842", 1))

	// The counts of the server have the messages under the name of the
	// inlet, by status, as the chapter shows them after these five.
	status, counts := ask(t, http.DefaultClient, http.MethodGet, base+"/v1/metrics", keys["olga"], "")
	for _, line := range strings.Split(wayIn(t, "the counts"), "\n") {
		if status != http.StatusOK || !strings.Contains(counts, line+"\n") {
			t.Errorf("the counts have no line %s", line)
		}
	}
	// And the lines of the server: a message that was carried out, one
	// the inlet did not act on, one without a proof, and one the
	// database refused, with the words the sender was not given.
	wrote := asShown(log.String())
	for _, line := range strings.Split(wayIn(t, "the lines"), "\n") {
		if !strings.Contains(wrote, asShown(line)+"\n") {
			t.Errorf("the server wrote no line like\n%s\nit wrote\n%s", line, wrote)
		}
	}
	for _, kept := range []string{secretOfTheBank, "ntc_8841", "Bank-Signature", "payment.received"} {
		if strings.Contains(log.String(), kept) {
			t.Errorf("a line of the server holds %q", kept)
		}
	}

	sent("a name that is no inlet", http.StatusNotFound, "bank.nothing", secretOfTheBank, other)
	sent("a day that is none", http.StatusUnprocessableEntity, "bank.paid", secretOfTheBank, strings.Replace(strings.Replace(other, "2026-11-16", "16.11.2026", 1), "ntc_8841", "ntc_8850", 1))
	sent("a message larger than the server reads", http.StatusRequestEntityTooLarge, "bank.paid", secretOfTheBank,
		`{"id": "ntc_8851", "padding": "`+strings.Repeat("x", httpdoor.DefaultMessageBytes)+`"}`)
	// The number of a message names one message.
	if status, body := notice(t, base, "bank.paid", secretOfTheBank, other); status != http.StatusConflict || body != `{"error":{"code":"key_reused"}}` {
		t.Errorf("another invoice under the number of the first notice: %d %s", status, body)
	}
	if status, body := send("olga", "set", `{"class": "Invoice", "id": "FV/2026/11/0044", "values": {"customer": "Initech"}, "key": "in:bank.paid:ntc_9000"}`); status != http.StatusUnprocessableEntity || !strings.Contains(body, "in:") {
		t.Errorf("a caller's key with the prefix of the inlets: %d %s", status, body)
	}
	if got := invoice("FV/2026/11/0044"); got["state"] != "sent" || got["version"] != float64(1) {
		t.Errorf("a refused message moved the other invoice: %v", got)
	}

	// Who may: every row of the chapter's table.
	def := `{"definition": ` + wayIn(t, "the inlet") + `}`
	message := `"message": ` + wayIn(t, "the sample")
	who := map[string]string{}
	for _, cells := range wayInRows(t, "who may") {
		for _, name := range strings.Split(cells[0], ", ") {
			who[strings.Trim(name, "`")] = cells[2]
		}
	}
	for _, c := range []struct{ name, object string }{
		{"validate_inlet", def}, {"try_inlet", `{"inlet": "bank.paid", ` + message + `}`}, {"define_inlet", def},
		{"get_inlet", `{"name": "bank.paid"}`}, {"list_inlets", `{}`}, {"remove_inlet", `{"name": "bank.paid"}`},
	} {
		allowed := map[string]bool{"olga": true, "dan": who[c.name] != "an administrator", "ann": who[c.name] == "every caller"}
		if who[c.name] == "" {
			t.Errorf("the chapter's table of who may has no %s", c.name)
		}
		// Olga last: her remove_inlet closes the inlet.
		for _, user := range []string{"ann", "dan", "olga"} {
			status, body := send(user, c.name, c.object)
			if (status == http.StatusOK) != allowed[user] || (!allowed[user] && status != http.StatusForbidden) {
				t.Errorf("%s by %s: %d %s, and the chapter says it is for %s", c.name, user, status, body, who[c.name])
			}
		}
	}
	// Removed, the address is closed.
	if status, body := notice(t, base, "bank.paid", secretOfTheBank, other); status != http.StatusNotFound || body != `{"error":{"code":"unknown_inlet"}}` {
		t.Errorf("a notice for an inlet that was removed: %d %s", status, body)
	}
	// Defined again, it is the next version; and the user is looked up
	// for every message: Olga disables bank-in, and the inlet is closed.
	if !strings.Contains(must("olga", "define_inlet", def), `"version":2`) {
		t.Error("an inlet defined again is not its next version")
	}
	must("olga", "set_user", `{"id": "bank-in", "disabled": true}`)
	if status, body := notice(t, base, "bank.paid", secretOfTheBank, strings.Replace(other, "ntc_8841", "ntc_8860", 1)); status != http.StatusNotFound || body != `{"error":{"code":"unknown_inlet"}}` {
		t.Errorf("a notice for an inlet whose user is disabled: %d %s", status, body)
	}
	if !strings.Contains(log.String(), "the user bank-in is disabled") {
		t.Errorf("the server did not write why the inlet acts as nobody:\n%s", log)
	}
	must("olga", "set_user", `{"id": "bank-in", "disabled": false}`)
	if code := stop(); code != 0 {
		t.Fatalf("the server ended with %d\n%s", code, log)
	}

	// When it starts, the server says which inlets it takes messages
	// at, and names one whose secret is in neither place.
	_, said2, stop2 := serving(t, served...)
	if want := wayIn(t, "what the server says"); !strings.Contains(said2.String(), want+"\n") {
		t.Errorf("the server said\n%s\nand the chapter shows\n%s", said2, want)
	}
	stop2()
	if err := os.Remove(filepath.Join(secrets, "bank", "signing_key")); err != nil {
		t.Fatal(err)
	}
	addr3, said3, stop3 := serving(t, served...)
	if !strings.Contains(said3.String(), "the inlet bank.paid proves its messages with the secret signing_key of the connection bank") {
		t.Errorf("a server whose secret is not there said\n%s", said3)
	}
	// Its senders are told to come back, and Tomek why.
	if status, body := notice(t, "http://"+addr3, "bank.paid", secretOfTheBank, strings.Replace(other, "ntc_8841", "ntc_8861", 1)); status != http.StatusServiceUnavailable || body != `{"error":{"code":"secret_unavailable"}}` {
		t.Errorf("a notice while the secret is not there: %d %s", status, body)
	}
	if strings.Contains(said3.String(), secretOfTheBank) || !strings.Contains(said3.String(), `"code":"secret_unavailable"`) {
		t.Errorf("the server whose secret is not there wrote\n%s", said3)
	}
	stop3()
	// Told nothing, a server takes no message: it says that the
	// database has inlets, answers every address as a name that is no
	// inlet, and takes no definition of one.
	addr4, said4, _ := serving(t, path, "--listen", "127.0.0.1:0")
	if !strings.Contains(said4.String(), "the database has inlets, and this server takes no messages from outside: bank.paid") {
		t.Errorf("a server that takes no messages said\n%s", said4)
	}
	if status, body := notice(t, "http://"+addr4, "bank.paid", secretOfTheBank, paid); status != http.StatusNotFound || body != `{"error":{"code":"unknown_inlet"}}` {
		t.Errorf("a notice to a server that takes no messages: %d %s", status, body)
	}
	status, body = ask(t, http.DefaultClient, http.MethodPost, "http://"+addr4+"/v1/define_inlet", keys["olga"], strings.Replace(def, "bank.paid", "bank.booked", 1))
	if status != http.StatusUnprocessableEntity || !strings.Contains(body, "not_supported") || !strings.Contains(body, "inbound") {
		t.Errorf("an inlet defined at a server that takes no messages: %d %s", status, body)
	}
}

// mustJSONText is a value as JSON, for a comparison.
func mustJSONText(t *testing.T, v any) string {
	t.Helper()
	text, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// The tables of the chapter say what the engine and the server have:
// the four names a formula reads, the three forms of a proof, the
// members of an inlet and the members of inbound with what is usual.
func TestTheTablesOfTheWayIn(t *testing.T) {
	names := func(mark string) string {
		var out []string
		for _, cells := range wayInRows(t, mark) {
			out = append(out, strings.Trim(cells[0], "`"))
		}
		return strings.Join(out, " ")
	}
	if got, want := names("what a formula reads"), strings.Join([]string{model.MessageBody, model.MessageHeaders, model.MessageParams, model.MessageRaw}, " "); got != want {
		t.Errorf("the chapter lists what a formula reads as %s, and the engine has %s", got, want)
	}
	if got, want := names("the forms of a proof"), strings.Join([]string{model.ProofShown, model.ProofBody, model.ProofTimestampBody}, " "); got != want {
		t.Errorf("the chapter lists the forms of a proof as %s, and the engine has %s", got, want)
	}
	// The members of an inlet are those the engine reads, and no other:
	// a definition with every one of them, and one with one more.
	var members []string
	for _, cells := range wayInRows(t, "the members of an inlet") {
		for _, name := range strings.Split(cells[0], ", ") {
			members = append(members, strings.Trim(name, "`"))
		}
	}
	var written map[string]json.RawMessage
	if err := json.Unmarshal([]byte(wayIn(t, "the inlet")), &written); err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		if _, has := written[member]; !has && member != "description" {
			t.Errorf("the chapter's table has the member %s, and its inlet has not", member)
		}
		// A member the engine does not know is refused as unknown; one
		// it knows, given a text where it takes an object, is refused
		// for that and not as unknown.
		probe, _ := json.Marshal(map[string]any{member: "x"})
		if _, diags := model.ParseInlet(probe); diags.Has(model.CodeBadJSON) && bytes.Contains([]byte(diags.Error()), []byte("unknown field")) {
			t.Errorf("the engine does not know the member %s of the chapter's table: %s", member, diags.Error())
		}
	}
	if len(members) != len(written)+1 {
		t.Errorf("the chapter's table has %d members, and its inlet %d and description", len(members), len(written))
	}
	if _, diags := model.ParseInlet([]byte(`{"name": "a.b", "secret": "x"}`)); !diags.Has(model.CodeBadJSON) {
		t.Error("the engine takes a member no inlet has")
	}

	// The members of inbound, and what is usual for each.
	usual := map[string]string{"inbound.message_bytes": strconv.Itoa(httpdoor.DefaultMessageBytes), "inbound.messages": strconv.Itoa(httpdoor.DefaultMessages)}
	rows := wayInRows(t, "the members of inbound")
	if len(rows) != len(inboundMembers)+1 || rows[0][0] != "`inbound`" {
		t.Fatalf("the chapter's table of inbound: %v", rows)
	}
	for _, member := range inboundMembers {
		found := false
		for _, cells := range rows {
			if cells[0] == "`inbound."+member+"`" {
				found = strings.HasPrefix(cells[2], usual["inbound."+member])
			}
		}
		if !found {
			t.Errorf("the chapter does not say what is usual for inbound.%s: %s", member, usual["inbound."+member])
		}
		if !strings.Contains(serveUsage, `"`+member+`"`) {
			t.Errorf("the help of the server does not show the setting inbound.%s", member)
		}
	}
	// The settings of the chapter are read, and turn the way in on; a
	// file without the member leaves it off.
	if s, err := parseSettings([]byte(wayIn(t, "the settings"))); err != nil || !s.inbound {
		t.Errorf("the settings of the chapter: %+v %v", s, err)
	}
	if s, err := parseSettings([]byte(`{"limits": {"commands": 8}}`)); err != nil || s.inbound {
		t.Errorf("settings without inbound: %+v %v", s, err)
	}
	if s, err := parseSettings([]byte(`{"inbound": {"message_bytes": 4096, "messages": 2}}`)); err != nil || !s.inbound || s.limits.MessageBytes != 4096 || s.limits.Messages != 2 {
		t.Errorf("settings with the limits of the messages: %+v %v", s, err)
	}
	for file, words := range map[string]string{
		`{"inbound": true}`:                  "inbound is an object",
		`{"inbound": {"inlets": ["a.b"]}}`:   `inbound has "inlets", which is no setting: it may have message_bytes, messages`,
		`{"inbound": {"messages": 0}}`:       "inbound.messages",
		`{"inbound": {"message_bytes": -1}}`: "inbound.message_bytes",
	} {
		if _, err := parseSettings([]byte(file)); err == nil || !strings.Contains(err.Error(), words) {
			t.Errorf("the settings %s: %v, want %q", file, err, words)
		}
	}
}

// "fadodb inlet check": what the engine would refuse of an inlet in a
// file, and what a message would become, storing nothing.
func TestInletCheck(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		t.Helper()
		at := filepath.Join(dir, name)
		if err := os.WriteFile(at, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return at
	}
	class, inlet, sample := write("invoice.json", wayIn(t, "the class")), write("bank-paid.json", wayIn(t, "the inlet")), write("notice.json", wayIn(t, "the sample"))

	// Without its class the inlet names a class the check has not, and
	// the check says how to give it.
	code, out, _ := tool("inlet", "check", inlet)
	if code != 1 || !strings.Contains(out, "unknown_class at /object/class") || !strings.Contains(out, "--class FILE") {
		t.Errorf("an inlet checked without its class: exit %d\n%s", code, out)
	}
	if code, out, said := tool("inlet", "check", inlet, "--class", class); code != 0 || out != inlet+": the engine takes it\n" || said != "" {
		t.Errorf("an inlet checked with its class: exit %d\n%s%s", code, out, said)
	}
	// What is wrong with an inlet, each with its code, its place and
	// the fix the engine knows.
	wrong := write("wrong.json", strings.Replace(strings.Replace(wayIn(t, "the inlet"), `"transition": "pay"`, `"transition": "settle"`, 1),
		`DATEVALUE(JSONVALUE(BODY, \"payment.booked\"))`, `JSONVALUE(BODY, \"payment.booked\")`, 1))
	code, out, _ = tool("inlet", "check", wrong, "--class", class, "--message", sample)
	if code != 1 || !strings.Contains(out, "2 findings refuse it") || !strings.Contains(out, "unknown_transition at /object/transition") ||
		!strings.Contains(out, "type_mismatch at /object/values/paid_on") || !strings.Contains(out, "fix: DATEVALUE") || strings.Contains(out, "the message") {
		t.Errorf("a wrong inlet: exit %d\n%s", code, out)
	}
	// A message the inlet does not act on, and one it cannot read.
	quiet := write("quiet.json", `{"body": {"type": "payment.returned"}}`)
	if code, out, _ := tool("inlet", "check", inlet, "--class", class, "--message", quiet); code != 0 || !strings.Contains(out, "the inlet does not act on it") {
		t.Errorf("a message of another kind: exit %d\n%s", code, out)
	}
	broken := write("broken.json", `{"body": {"type": "payment.received", "payment": {"reference": "FV/1", "booked": "tomorrow"}}}`)
	if code, out, _ := tool("inlet", "check", inlet, "--class", class, "--message", broken); code != 1 || !strings.Contains(out, "the inlet cannot read it") || !strings.Contains(out, "bad_message at /key") {
		t.Errorf("a message the inlet cannot read: exit %d\n%s", code, out)
	}
	// For a program: what the two commands answer, each on a line.
	code, out, _ = tool("inlet", "check", inlet, "--class", class, "--message", sample, "--json")
	answers := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(answers) != 2 || answers[0] != `{"diagnostics":[]}` || !strings.HasPrefix(answers[1], `{"trial":{"inlet":"bank.paid","as":"bank-in","acts":true,"command":"transition"`) {
		t.Errorf("the check for a program: exit %d\n%s", code, out)
	}
	if code, out, _ := tool("inlet", "check", wrong, "--class", class, "--json"); code != 1 || !strings.HasPrefix(out, `{"diagnostics":[{"code":"unknown_transition"`) {
		t.Errorf("a wrong inlet, for a program: exit %d\n%s", code, out)
	}

	// Against a database: its classes and its users are what the inlet
	// may name, and nothing is written to it.
	path := filepath.Join(dir, "acme.fado")
	if code, _, said := tool("init", path); code != 0 {
		t.Fatal(said)
	}
	code, out, _ = tool("inlet", "check", inlet, "--db", path)
	if code != 1 || !strings.Contains(out, "unknown_class at /object/class") || strings.Contains(out, "--class FILE") {
		t.Errorf("an inlet checked against a database without its class: exit %d\n%s", code, out)
	}
	// A served database is asked through its server.
	_, _, stop := serving(t, path, "--listen", "127.0.0.1:0")
	if code, _, said := tool("inlet", "check", inlet, "--db", path); code != 1 || !strings.Contains(said, "in_use") || !strings.Contains(said, "validate_inlet") {
		t.Errorf("an inlet checked against a served database: exit %d\n%s", code, said)
	}
	stop()

	// The lines the tool cannot read, and its help.
	for what, args := range map[string][]string{
		"no file":             {"inlet", "check"},
		"two files":           {"inlet", "check", inlet, wrong},
		"a class and a base":  {"inlet", "check", inlet, "--class", class, "--db", path},
		"a word that is none": {"inlet", "define", inlet},
	} {
		if code, _, said := tool(args...); code != 2 || !strings.Contains(said, "fadodb inlet check FILE") {
			t.Errorf("%s: exit %d\n%s", what, code, said)
		}
	}
	if code, _, said := tool("inlet", "check", filepath.Join(dir, "nothing.json")); code != 2 || !strings.Contains(said, "nothing.json") {
		t.Errorf("a file that is not there: exit %d\n%s", code, said)
	}
	if code, _, said := tool("inlet", "check", inlet, "--db", filepath.Join(dir, "nothing.fado")); code != 1 || !strings.Contains(said, "fadodb init") {
		t.Errorf("a database that is not there: exit %d\n%s", code, said)
	}
	if code, _, said := tool("inlet", "check", "--help"); code != 0 || said != inletUsage {
		t.Errorf("the help: exit %d\n%s", code, said)
	}
}
