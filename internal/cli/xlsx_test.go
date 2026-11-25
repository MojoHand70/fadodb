package cli

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readSheet opens an Excel file as any program does, a zip of XML, and
// answers its cells by their place: what kind each holds, its value,
// and for a number how it is shown. Every part must be XML.
func readSheet(t *testing.T, file []byte) (name string, cells map[string]string) {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatalf("the file is no zip: %v", err)
	}
	parts := map[string][]byte{}
	for _, part := range archive.File {
		from, err := part.Open()
		if err != nil {
			t.Fatal(err)
		}
		parts[part.Name], _ = io.ReadAll(from)
		from.Close()
		var any struct{}
		if err := xml.Unmarshal(parts[part.Name], &any); err != nil {
			t.Fatalf("%s is not XML: %v", part.Name, err)
		}
	}
	for _, needed := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/worksheets/sheet1.xml"} {
		if parts[needed] == nil {
			t.Fatalf("the file has no %s", needed)
		}
	}
	var book struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
		} `xml:"sheets>sheet"`
	}
	var styles struct {
		Formats []struct {
			ID   int    `xml:"numFmtId,attr"`
			Code string `xml:"formatCode,attr"`
		} `xml:"numFmts>numFmt"`
		Looks []struct {
			Format int `xml:"numFmtId,attr"`
			Font   int `xml:"fontId,attr"`
		} `xml:"cellXfs>xf"`
	}
	var sheet struct {
		Pane struct {
			Frozen int `xml:"ySplit,attr"`
		} `xml:"sheetViews>sheetView>pane"`
		Rows []struct {
			Cells []struct {
				At    string `xml:"r,attr"`
				Look  int    `xml:"s,attr"`
				Kind  string `xml:"t,attr"`
				Value string `xml:"v"`
				Text  string `xml:"is>t"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	for part, into := range map[string]any{"xl/workbook.xml": &book, "xl/styles.xml": &styles, "xl/worksheets/sheet1.xml": &sheet} {
		if err := xml.Unmarshal(parts[part], into); err != nil {
			t.Fatalf("%s: %v", part, err)
		}
	}
	if len(book.Sheets) != 1 {
		t.Fatalf("the file has %d sheets", len(book.Sheets))
	}
	if sheet.Pane.Frozen != 1 {
		t.Errorf("the first line of the sheet is not kept in sight")
	}
	codes := map[int]string{0: "", 14: "day", 22: "moment"}
	for _, f := range styles.Formats {
		codes[f.ID] = f.Code
	}
	cells = map[string]string{}
	for _, row := range sheet.Rows {
		for _, c := range row.Cells {
			if c.Look >= len(styles.Looks) {
				t.Fatalf("%s has a look the file does not describe: %d", c.At, c.Look)
			}
			look := styles.Looks[c.Look]
			shown, known := codes[look.Format]
			if !known {
				t.Fatalf("%s is shown by a format the file does not describe: %d", c.At, look.Format)
			}
			switch {
			case c.Kind == "inlineStr" && look.Font == 1:
				cells[c.At] = "name: " + c.Text
			case c.Kind == "inlineStr":
				cells[c.At] = "text: " + c.Text
			case c.Kind == "b":
				cells[c.At] = "bool: " + c.Value
			case shown == "":
				cells[c.At] = "number: " + c.Value
			default:
				cells[c.At] = "number: " + c.Value + " shown as " + shown
			}
		}
	}
	return book.Sheets[0].Name, cells
}

// An export for a person: an Excel file in which a number is a number
// and a day a day, by the type the class gives each variable, so that
// nothing in the file is written in anybody's locale.
func TestExecWritesAnExcelFile(t *testing.T) {
	asOlga(t)
	asked := []string{}
	var server *answering
	server = answeringWith(t, func(w http.ResponseWriter, r *http.Request) {
		body := server.body
		switch r.URL.Path {
		case "/v1/query":
			io.WriteString(w, `{"documents":[`+
				`{"class":"Invoice","id":"FV/2026/11/0041","state":"approved","class_version":2,"values":{"supplier":"Ink & Son <Gdańsk>","total":"1250.50","net":"1016.67 PLN","pieces":"12","weight":3.75,"due":"2026-12-15","seen":1794778200,"paid":true,"code":"10.50","gross":"1250.50 PLN","count":"3","lines":[{"sku":"A4"}]}},`+
				`{"class":"Invoice","id":"FV/2026/11/0042","state":"review","class_version":1,"values":{"supplier":"Paper Mill","total":"840.00","net":"-80.00 EUR","paid":false,"code":"0042","gross":"0042","note":null}}`+
				`]}`)
		case "/v1/get_class":
			asked = append(asked, body)
			// The second version gave total, pieces and the others their
			// types; the first knew total as a text.
			total := `"decimal", "scale": 2`
			if strings.Contains(body, `"version":1`) {
				total = `"text"`
			}
			io.WriteString(w, `{"class":{"name":"Invoice","definition":{"name":"Invoice","variables":{`+
				`"supplier":{"type":"text"},"total":{"type":`+total+`},"net":{"type":"money","scale":2},"pieces":{"type":"int"},`+
				`"weight":{"type":"number"},"due":{"type":"date"},"seen":{"type":"datetime"},"paid":{"type":"bool"},"code":{"type":"text"},`+
				`"gross":{"formula":"net * 1.23"},"count":{"formula":"COUNT(lines)"},"lines":{"type":"rows","schema":{"sku":"text"}}}}}}`)
		case "/v1/sql":
			io.WriteString(w, `{"columns":["id","total","note"],"rows":[["FV/2026/11/0041",125050,null],["FV/2026/11/0042",84000.5,"10.50"]]}`)
		case "/v1/refused":
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error":{"code":"forbidden","message":"olga may not"}}`)
		}
	})
	dir := t.TempDir()
	file := filepath.Join(dir, "invoices.xlsx")
	code, out, said := tool("exec", "query", `{"class": "Invoice"}`, "--xlsx", "--out", file, "--url", server.URL)
	if code != exitDone || out != "" || said != "" {
		t.Fatalf("exit %d, printed %q, said %q", code, out, said)
	}
	// The class was asked for once in each version the documents are of.
	if len(asked) != 2 || asked[0] != `{"name":"Invoice","version":2}` || asked[1] != `{"name":"Invoice","version":1}` {
		t.Errorf("the tool asked for the class as %q", asked)
	}
	written, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	name, cells := readSheet(t, written)
	if name != "Invoice" {
		t.Errorf("the sheet is named %q", name)
	}
	for at, want := range map[string]string{
		"A1": "name: id", "B1": "name: state", "C1": "name: supplier", "D1": "name: total", "O1": "name: note",
		"A2": "text: FV/2026/11/0041",
		"B2": "text: approved",
		"C2": "text: Ink & Son <Gdańsk>",
		"D2": "number: 1250.50 shown as 0.00",            // a decimal
		"E2": `number: 1016.67 shown as #,##0.00 "PLN"`,  // an amount, with its currency
		"F2": "number: 12 shown as 0",                    // a whole number
		"G2": "number: 3.75",                             // a plain number
		"H2": "number: 46371 shown as day",               // 2026-12-15
		"I2": "number: 46341.8958333333 shown as moment", // 2026-11-15 21:30:00 UTC
		"J2": "bool: 1",                                  //
		"K2": "text: 10.50",                              // a text stays a text, whatever it looks like
		"L2": `number: 1250.50 shown as #,##0.00 "PLN"`,  // computed, no type named: told by its form
		"M2": "number: 3 shown as 0",                     // computed, no type named
		"N2": `text: [{"sku":"A4"}]`,                     // rows, as their JSON
		"D3": "text: 840.00",                             // in version 1 of the class total was a text
		"E3": `number: -80.00 shown as #,##0.00 "EUR"`,   //
		"J3": "bool: 0",                                  //
		"K3": "text: 0042",                               //
		"L3": "text: 0042",                               // digits that begin with a zero are no number
	} {
		if cells[at] != want {
			t.Errorf("%s holds %q, want %q", at, cells[at], want)
		}
	}
	for _, empty := range []string{"O2", "O3", "F3", "H3", "N3"} {
		if held, there := cells[empty]; there {
			t.Errorf("%s holds %q, want nothing", empty, held)
		}
	}
	// The same answer makes the same file, byte for byte.
	again := filepath.Join(dir, "again.xlsx")
	tool("exec", "query", `{"class": "Invoice"}`, "--xlsx", "--out", again, "--url", server.URL)
	if second, _ := os.ReadFile(again); !bytes.Equal(written, second) {
		t.Error("the same answer made two files that differ")
	}

	// With fields, the columns are those; a row of SQL has no class, so
	// a number is a number and a text a text.
	tool("exec", "query", `{"class": "Invoice", "fields": ["total", "supplier"]}`, "--xlsx", "--out", file, "--url", server.URL)
	written, _ = os.ReadFile(file)
	if _, cells := readSheet(t, written); cells["C1"] != "name: total" || cells["D1"] != "name: supplier" || cells["E1"] != "" || cells["C2"] != "number: 1250.50 shown as 0.00" {
		t.Errorf("with fields the sheet holds %v", cells)
	}
	code, _, said = tool("exec", "sql", `{"statement": "SELECT id, total, note FROM Invoice"}`, "--xlsx", "--out", file, "--url", server.URL)
	written, _ = os.ReadFile(file)
	name, cells = readSheet(t, written)
	if code != exitDone || name != "Rows" || cells["A1"] != "name: id" || cells["B2"] != "number: 125050" || cells["B3"] != "number: 84000.5" || cells["C3"] != "text: 10.50" || cells["C2"] != "" {
		t.Errorf("sql as an Excel file: exit %d, said %q, sheet %q with %v", code, said, name, cells)
	}

	// A refusal writes no file, and leaves the one that was there.
	before, _ := os.ReadFile(file)
	none := filepath.Join(dir, "none.xlsx")
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"error":{"code":"forbidden","message":"olga may not read Invoice"}}`)
	})
	for _, to := range []string{none, file} {
		if code, _, said := tool("exec", "query", `{"class": "Invoice"}`, "--xlsx", "--out", to, "--url", server.URL); code != exitRefused || !strings.Contains(said, "forbidden") {
			t.Errorf("a refused export: exit %d, said %q", code, said)
		}
	}
	if _, err := os.Stat(none); err == nil {
		t.Error("a refused export wrote a file")
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
		t.Error("a refused export changed the file that was there")
	}
}

// --out writes what the tool would print to a file, which is how an
// answer reaches a file in a shell that changes what passes through it.
func TestExecWritesToAFile(t *testing.T) {
	asOlga(t)
	server := answeringWith(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/query":
			io.WriteString(w, `{"documents":[{"id":"A","state":"draft","values":{"supplier":"Zażółć"}}]}`)
		case "/v1/subscribe":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, ": the stream is open\n\nevent: change\ndata: {\"event_id\":\"E1\"}\n\nevent: end\ndata: {\"error\":{\"code\":\"closed\",\"message\":\"the server is stopping\"}}\n\n")
		default:
			io.WriteString(w, `{"classes":["Invoice"]}`)
		}
	})
	dir := t.TempDir()
	for _, c := range []struct {
		args []string
		want string
		code int
	}{
		{[]string{"list_classes"}, "{\n  \"classes\": [\n    \"Invoice\"\n  ]\n}\n", exitDone},
		{[]string{"query", `{"class": "Invoice"}`, "--csv"}, "id,state,supplier\nA,draft,Zażółć\n", exitDone},
		{[]string{"subscribe"}, "{\"event_id\":\"E1\"}\n", exitRefused},
	} {
		file := filepath.Join(dir, c.args[0]+".out")
		code, out, _ := tool(append([]string{"exec", "--url", server.URL, "--out", file}, c.args...)...)
		written, err := os.ReadFile(file)
		if code != c.code || out != "" || err != nil || string(written) != c.want {
			t.Errorf("%v: exit %d, printed %q, the file holds %q (%v), want %q", c.args, code, out, written, err, c.want)
		}
	}
	// A file that cannot be written is an answer that did not come
	// through.
	if code, _, said := tool("exec", "list_classes", "--url", server.URL, "--out", filepath.Join(dir, "no", "such", "place")); code != exitNoAnswer || !strings.Contains(said, "could not be written") {
		t.Errorf("a file that cannot be written: exit %d, said %q", code, said)
	}
}

// A cell of an Excel file, by the type of its variable and its value.
func TestACellOfAnExcelFile(t *testing.T) {
	number := func(text string, digits int, unit string) sheetCell {
		return sheetCell{kind: cellNumber, text: text, digits: digits, unit: unit}
	}
	for _, c := range []struct {
		declared, value string
		want            sheetCell
	}{
		{"text", `"Paper Mill"`, textCell("Paper Mill")},
		{"text", `"1250.50"`, textCell("1250.50")},
		{"text", `"=SUM(A1:A9)"`, textCell("=SUM(A1:A9)")},
		{"decimal", `"1250.50"`, number("1250.50", 2, "")},
		{"decimal", `"-0.2300"`, number("-0.2300", 4, "")},
		{"decimal", `"7"`, number("7", 0, "")},
		{"decimal", `"123456789012345.67"`, textCell("123456789012345.67")}, // more digits than a number of the file keeps
		{"decimal", `"0.000000000000001"`, number("0.000000000000001", 15, "")},
		{"int", `"12"`, number("12", 0, "")},
		{"int", `"1234567890123456"`, textCell("1234567890123456")},
		{"money", `"1250.50 PLN"`, number("1250.50", 2, "PLN")},
		{"money", `"-80 JPY"`, number("-80", 0, "JPY")},
		{"money", `"1250.50"`, textCell("1250.50")}, // an amount has a currency
		{"money", `"1234567890123456.00 PLN"`, textCell("1234567890123456.00 PLN")},
		{"number", `3.75`, number("3.75", -1, "")},
		{"number", `1e21`, number("1e21", -1, "")},
		{"number", `12345678901234567890`, textCell("12345678901234567890")},
		{"date", `"2026-12-15"`, sheetCell{kind: cellDay, text: "46371"}},
		{"date", `"1900-03-01"`, sheetCell{kind: cellDay, text: "61"}},
		{"date", `"1900-02-28"`, textCell("1900-02-28")}, // before the first day the file counts right
		{"date", `"soon"`, textCell("soon")},
		{"datetime", `1794778200`, sheetCell{kind: cellMoment, text: "46341.8958333333"}},
		{"datetime", `"2026-11-15 21:30:00"`, sheetCell{kind: cellMoment, text: "46341.8958333333"}},
		{"datetime", `"2026-11-15 00:00:00"`, sheetCell{kind: cellMoment, text: "46341"}},
		{"bool", `true`, sheetCell{kind: cellBool, text: "1"}},
		{"bool", `false`, sheetCell{kind: cellBool, text: "0"}},
		{"rows", `[{"sku": "A4"}]`, textCell(`[{"sku":"A4"}]`)},
		{"object", `{"city": "Gdańsk"}`, textCell(`{"city":"Gdańsk"}`)},
		{"ref", `"Customer:C-17"`, textCell("Customer:C-17")},
		{"decimal", `null`, sheetCell{}},
		{"decimal", ``, sheetCell{}},
		// A computed variable whose definition names no type: by the
		// form the engine writes a value in.
		{"", `"1250.50 PLN"`, number("1250.50", 2, "PLN")},
		{"", `"1250.50"`, number("1250.50", 2, "")},
		{"", `"12"`, number("12", 0, "")},
		{"", `"0"`, number("0", 0, "")},
		{"", `"0042"`, textCell("0042")},
		{"", `"2026-12-15"`, sheetCell{kind: cellDay, text: "46371"}},
		{"", `"2026-11-15 21:30:00"`, sheetCell{kind: cellMoment, text: "46341.8958333333"}},
		{"", `"Paper Mill"`, textCell("Paper Mill")},
		{"", `"1,250.50"`, textCell("1,250.50")},
		{"", `3.75`, number("3.75", -1, "")},
		{"", `true`, sheetCell{kind: cellBool, text: "1"}},
		// A row of SQL.
		{asWritten, `125050`, number("125050", -1, "")},
		{asWritten, `"10.50"`, textCell("10.50")},
	} {
		if got := cellOf(c.declared, json.RawMessage(c.value)); got != c.want {
			t.Errorf("a %q value %s is the cell %+v, want %+v", c.declared, c.value, got, c.want)
		}
	}

	for i, want := range map[int]string{0: "A", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA", 701: "ZZ", 702: "AAA", 16383: "XFD"} {
		if got := columnName(i); got != want {
			t.Errorf("column %d is named %s, want %s", i, got, want)
		}
	}
	for name, want := range map[string]string{"Invoice": "Invoice", "": "Sheet1", "a/b:c*d?e[f]g\\h": "abcdefgh", "'quoted'": "quoted",
		"a name of a class that is longer than a sheet takes": "a name of a class that is longe", "Zażółć gęślą": "Zażółć gęślą"} {
		if got := sheetName(name); got != want {
			t.Errorf("a sheet for %q is named %q, want %q", name, got, want)
		}
	}
	for text, want := range map[string]string{"a < b & c > \"d\" 'e'": "a &lt; b &amp; c &gt; &quot;d&quot; &apos;e&apos;", "line\nbreak\ttab": "line\nbreak\ttab",
		"bell\x07 and\rreturn": "bell_x0007_ and_x000D_return", "zażółć": "zażółć"} {
		if got := xmlText(text); got != want {
			t.Errorf("xmlText(%q) = %q, want %q", text, got, want)
		}
	}
	// A sheet holds so many lines and no more.
	if err := writeXLSX(io.Discard, "Big", []string{"id"}, make([][]sheetCell, mostRows)); err == nil || !strings.Contains(err.Error(), "ask for fewer with limit") {
		t.Errorf("a sheet with more lines than it holds: %v", err)
	}
}
