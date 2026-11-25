package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MojoHand70/fadodb/internal/command"
)

// An export for a person (ADR-0062, item 7): an Excel file. A CSV file
// is text, so whoever writes one has to choose a mark between the
// values and a mark inside a number, and the reader's spreadsheet
// expects the marks of the reader's language. An Excel file holds no
// such mark: a number is kept as a number and a day as a day, and the
// program that opens the file shows them in the reader's own way. So
// nothing in the export is anybody's guess of a locale, neither the
// server's nor the tool's.
//
// The file is written here, with the standard library: a zip of six
// small XML files (ADR-0002).

// cellKind is what a cell of a sheet holds.
type cellKind byte

const (
	cellEmpty cellKind = iota
	cellText
	cellNumber
	cellBool
	cellDay
	cellMoment
)

// sheetCell is one cell of an export.
type sheetCell struct {
	kind cellKind
	// text is the text of a text; the digits of a number, a day or a
	// moment, as the file keeps them; 1 or 0 for true or false.
	text string
	// digits is how many digits after the point a number is shown
	// with; -1 leaves it to the program that opens the file.
	digits int
	// unit is the currency of an amount.
	unit string
}

var (
	exactNumber = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	amount      = regexp.MustCompile(`^(-?\d+(?:\.\d+)?) ([A-Z]{3})$`)
	pointNumber = regexp.MustCompile(`^-?\d+\.\d+$`)
	wholeNumber = regexp.MustCompile(`^-?(0|[1-9]\d*)$`)
	dayForm     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	momentForm  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)
)

// mostDigits is how many digits a number of an Excel file keeps. One
// with more is written as a text, so that no digit is lost.
const mostDigits = 15

// textCell is a cell with a text.
func textCell(text string) sheetCell { return sheetCell{kind: cellText, text: text} }

// numberCell is a cell with an exact number: its digits as the engine
// wrote them, shown with as many digits after the point as it has, and
// with its currency when it is an amount. A number an Excel file cannot
// keep whole is a text.
func numberCell(digits, unit string) sheetCell {
	shown := digits
	if unit != "" {
		shown += " " + unit
	}
	if !exactNumber.MatchString(digits) || significant(digits) > mostDigits {
		return textCell(shown)
	}
	after := 0
	if i := strings.IndexByte(digits, '.'); i >= 0 {
		after = len(digits) - i - 1
	}
	return sheetCell{kind: cellNumber, text: digits, digits: after, unit: unit}
}

// plainNumber is a cell with a number as a spreadsheet keeps one: the
// program that opens the file chooses how to show it.
func plainNumber(written string) sheetCell {
	if _, err := strconv.ParseFloat(written, 64); err != nil {
		return textCell(written)
	}
	if exactNumber.MatchString(written) && significant(written) > mostDigits {
		return textCell(written)
	}
	return sheetCell{kind: cellNumber, text: written, digits: -1}
}

// significant counts the digits of a number from its first that is not
// a zero.
func significant(digits string) int {
	n := 0
	for _, r := range strings.TrimLeft(strings.TrimPrefix(digits, "-"), "0.") {
		if r != '.' {
			n++
		}
	}
	return n
}

// firstDay is the first day an Excel file counts right: it counts a
// 29th of February 1900 that never was.
var firstDay = time.Date(1900, 3, 1, 0, 0, 0, 0, time.UTC)

// dayZero is the day an Excel file counts days from.
var dayZero = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)

// dayCell is a cell with a calendar day, written as "2026-12-01".
func dayCell(written string) sheetCell {
	day, err := time.Parse("2006-01-02", written)
	if err != nil || day.Before(firstDay) {
		return textCell(written)
	}
	return sheetCell{kind: cellDay, text: strconv.Itoa(int(day.Sub(dayZero).Hours() / 24))}
}

// momentCell is a cell with a moment, written as "2026-12-01 09:30:00"
// in UTC or, as an answer writes it, in seconds since 1970. The cell
// keeps it in UTC.
func momentCell(written string) sheetCell {
	moment, err := time.Parse("2006-01-02 15:04:05", written)
	if err != nil {
		seconds, bad := strconv.ParseInt(written, 10, 64)
		if bad != nil {
			return textCell(written)
		}
		moment = time.Unix(seconds, 0).UTC()
	}
	if moment.Before(firstDay) || moment.Year() > 9999 {
		return textCell(written)
	}
	seconds := int64(moment.Sub(dayZero) / time.Second)
	days, rest := seconds/86400, seconds%86400
	serial := strconv.FormatInt(days, 10)
	if rest != 0 {
		serial += strings.TrimPrefix(strconv.FormatFloat(float64(rest)/86400, 'f', 10, 64), "0")
	}
	return sheetCell{kind: cellMoment, text: serial}
}

// cellOf is the cell of a value of a document. declared is the type the
// class gives the variable; it is empty for a computed variable whose
// definition names no type, and such a value is told by its form.
func cellOf(declared string, value json.RawMessage) sheetCell {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || string(value) == "null" {
		return sheetCell{}
	}
	switch value[0] {
	case '{', '[':
		return textCell(cell(value))
	case 't', 'f':
		if string(value) == "true" {
			return sheetCell{kind: cellBool, text: "1"}
		}
		return sheetCell{kind: cellBool, text: "0"}
	case '"':
		text := cell(value)
		switch declared {
		case "decimal", "int":
			return numberCell(text, "")
		case "money":
			if m := amount.FindStringSubmatch(text); m != nil {
				return numberCell(m[1], m[2])
			}
		case "date":
			return dayCell(text)
		case "datetime":
			return momentCell(text)
		case "":
			return cellByForm(text)
		}
		return textCell(text)
	}
	// A JSON number: a whole number, a plain one, or a moment, which an
	// answer writes in seconds.
	switch declared {
	case "text":
		return textCell(string(value))
	case "datetime":
		return momentCell(string(value))
	}
	return plainNumber(string(value))
}

// cellByForm tells what a text is by how the engine writes it, for a
// computed variable whose definition names no type: an amount, an exact
// number, a whole number, a day and a moment each have one form. A text
// of digits that begins with a zero, such as 0042, is no number. A
// designer who means a text gives the variable the type text.
func cellByForm(text string) sheetCell {
	switch {
	case amount.MatchString(text):
		m := amount.FindStringSubmatch(text)
		return numberCell(m[1], m[2])
	case pointNumber.MatchString(text), wholeNumber.MatchString(text):
		return numberCell(text, "")
	case dayForm.MatchString(text):
		return dayCell(text)
	case momentForm.MatchString(text):
		return momentCell(text)
	}
	return textCell(text)
}

// asWritten is the type of a value that has no class to give it one,
// as a row of SQL: a number is a number and a text a text.
const asWritten = "as written"

// classTypes are the types a class gives its variables, for each
// version of the class the exported documents are of.
type classTypes struct {
	byVersion map[int]map[string]string
}

// of is the type of a variable in a version of the class; empty when
// the definition names none.
func (t *classTypes) of(version int, variable string) string {
	if t == nil {
		return ""
	}
	return t.byVersion[version][variable]
}

// typesOf asks the server for the definition of the class of a query,
// in each version its documents are of, and reads the type of every
// variable. Reading a definition is open to every caller (ADR-0051).
func typesOf(ctx context.Context, c *caller, object, body []byte) (*classTypes, int) {
	var asked struct {
		Class string `json:"class"`
	}
	var answer struct {
		Documents []struct {
			ClassVersion int `json:"class_version"`
		} `json:"documents"`
	}
	_ = json.Unmarshal(object, &asked)
	_ = json.Unmarshal(body, &answer)
	types := &classTypes{byVersion: map[int]map[string]string{}}
	for _, d := range answer.Documents {
		if _, known := types.byVersion[d.ClassVersion]; known {
			continue
		}
		get := map[string]any{"name": asked.Class}
		if d.ClassVersion > 0 {
			get["version"] = d.ClassVersion
		}
		object, _ := command.Marshal(get)
		read, code := c.command(ctx, "get_class", object)
		if code != exitDone {
			return nil, code
		}
		var class struct {
			Class struct {
				Definition struct {
					Variables map[string]struct {
						Type json.RawMessage `json:"type"`
					} `json:"variables"`
				} `json:"definition"`
			} `json:"class"`
		}
		if err := json.Unmarshal(read, &class); err != nil {
			fmt.Fprintf(c.stderr, "%s: the definition of %s could not be read: %v\n", c.name, asked.Class, err)
			return nil, exitNoAnswer
		}
		declared := map[string]string{}
		for name, variable := range class.Class.Definition.Variables {
			var named string
			if json.Unmarshal(variable.Type, &named) == nil {
				declared[name] = named
			}
		}
		types.byVersion[d.ClassVersion] = declared
	}
	return types, exitDone
}

// exportXLSX writes the answer of query or of sql as an Excel file: a
// line of names, then a line for each document or row.
func exportXLSX(w io.Writer, sent string, object, body []byte, types *classTypes) error {
	if sent == "sql" {
		var answer struct {
			Columns []string            `json:"columns"`
			Rows    [][]json.RawMessage `json:"rows"`
		}
		if err := json.Unmarshal(body, &answer); err != nil {
			return fmt.Errorf("the answer of sql could not be read as columns and rows: %v", err)
		}
		rows := make([][]sheetCell, len(answer.Rows))
		for i, row := range answer.Rows {
			for _, value := range row {
				rows[i] = append(rows[i], cellOf(asWritten, value))
			}
		}
		return writeXLSX(w, "Rows", answer.Columns, rows)
	}
	var asked struct {
		Class string `json:"class"`
	}
	_ = json.Unmarshal(object, &asked)
	variables, documents, err := exported(object, body)
	if err != nil {
		return err
	}
	rows := make([][]sheetCell, len(documents))
	for i, d := range documents {
		rows[i] = append(rows[i], textCell(d.ID), textCell(d.State))
		for _, v := range variables {
			rows[i] = append(rows[i], cellOf(types.of(d.ClassVersion, v), d.values[v]))
		}
	}
	return writeXLSX(w, asked.Class, append([]string{"id", "state"}, variables...), rows)
}

// The most a sheet of an Excel file holds.
const (
	mostRows    = 1048576
	mostColumns = 16384
)

// writeXLSX writes one sheet as an Excel file: the names in its first
// line, which stays in sight, and the rows under it.
func writeXLSX(w io.Writer, sheet string, header []string, rows [][]sheetCell) error {
	if len(rows)+1 > mostRows {
		return fmt.Errorf("an Excel sheet holds %d lines and the answer has %d: ask for fewer with limit, or write --csv", mostRows-1, len(rows))
	}
	if len(header) > mostColumns {
		return fmt.Errorf("an Excel sheet holds %d columns and the answer has %d: ask for fewer with fields", mostColumns, len(header))
	}
	// The looks of a cell: 0 as it comes, 1 a name of the first line,
	// 2 a day, 3 a moment, and from 4 on one for each way a number is
	// shown, in the order they are met.
	const firstFormat, firstFormatID = 4, 164
	var formats []string
	looks := map[string]int{}
	look := func(c sheetCell) int {
		switch {
		case c.kind == cellDay:
			return 2
		case c.kind == cellMoment:
			return 3
		case c.kind != cellNumber || c.digits < 0:
			return 0
		}
		code := "0"
		if c.unit != "" {
			code = "#,##0"
		}
		if c.digits > 0 {
			code += "." + strings.Repeat("0", c.digits)
		}
		if c.unit != "" {
			code += ` "` + c.unit + `"`
		}
		if _, known := looks[code]; !known {
			looks[code] = firstFormat + len(formats)
			formats = append(formats, code)
		}
		return looks[code]
	}

	widths := make([]int, len(header))
	wide := func(column int, shown string) {
		if n := utf8.RuneCountInString(shown); column < len(widths) && n > widths[column] {
			widths[column] = n
		}
	}
	var data bytes.Buffer
	data.WriteString(`<row r="1">`)
	for i, name := range header {
		wide(i, name)
		fmt.Fprintf(&data, `<c r="%s1" s="1" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, columnName(i), xmlText(name))
	}
	data.WriteString(`</row>`)
	for r, row := range rows {
		fmt.Fprintf(&data, `<row r="%d">`, r+2)
		for i, c := range row {
			at := columnName(i) + strconv.Itoa(r+2)
			switch c.kind {
			case cellEmpty:
			case cellText:
				wide(i, c.text)
				fmt.Fprintf(&data, `<c r="%s" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, at, xmlText(c.text))
			case cellBool:
				// The word a program shows for true or false is its
				// reader's, and may be longer than the English one.
				wide(i, "VERDADEIRO")
				fmt.Fprintf(&data, `<c r="%s" t="b"><v>%s</v></c>`, at, c.text)
			default:
				shown := c.text
				switch c.kind {
				case cellDay:
					shown = "2026-12-01"
				case cellMoment:
					shown = "2026-12-01 09:30"
				}
				wide(i, shown+" "+c.unit)
				if s := look(c); s != 0 {
					fmt.Fprintf(&data, `<c r="%s" s="%d"><v>%s</v></c>`, at, s, c.text)
				} else {
					fmt.Fprintf(&data, `<c r="%s"><v>%s</v></c>`, at, c.text)
				}
			}
		}
		data.WriteString(`</row>`)
	}

	const head = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	const main = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"`
	var page bytes.Buffer
	page.WriteString(head + `<worksheet ` + main + `><sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`)
	if len(widths) > 0 {
		page.WriteString(`<cols>`)
		for i, n := range widths {
			fmt.Fprintf(&page, `<col min="%d" max="%d" width="%d" customWidth="1"/>`, i+1, i+1, min(max(n, 6), 60)+2)
		}
		page.WriteString(`</cols>`)
	}
	page.WriteString(`<sheetData>`)
	page.Write(data.Bytes())
	page.WriteString(`</sheetData></worksheet>`)

	var styles bytes.Buffer
	styles.WriteString(head + `<styleSheet ` + main + `>`)
	if len(formats) > 0 {
		fmt.Fprintf(&styles, `<numFmts count="%d">`, len(formats))
		for i, code := range formats {
			fmt.Fprintf(&styles, `<numFmt numFmtId="%d" formatCode="%s"/>`, firstFormatID+i, xmlText(code))
		}
		styles.WriteString(`</numFmts>`)
	}
	styles.WriteString(`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
		`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
		`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
		`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>`)
	fmt.Fprintf(&styles, `<cellXfs count="%d">`, firstFormat+len(formats))
	styles.WriteString(`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
		`<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>` +
		`<xf numFmtId="14" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>` +
		`<xf numFmtId="22" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>`)
	for i := range formats {
		fmt.Fprintf(&styles, `<xf numFmtId="%d" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>`, firstFormatID+i)
	}
	styles.WriteString(`</cellXfs><cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`)

	const relationships = `xmlns="http://schemas.openxmlformats.org/package/2006/relationships"`
	const document = `http://schemas.openxmlformats.org/officeDocument/2006/relationships`
	const kind = `application/vnd.openxmlformats-officedocument.spreadsheetml.`
	parts := []struct {
		name string
		text []byte
	}{
		{"[Content_Types].xml", []byte(head + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/xl/workbook.xml" ContentType="` + kind + `sheet.main+xml"/>` +
			`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="` + kind + `worksheet+xml"/>` +
			`<Override PartName="/xl/styles.xml" ContentType="` + kind + `styles+xml"/></Types>`)},
		{"_rels/.rels", []byte(head + `<Relationships ` + relationships + `>` +
			`<Relationship Id="rId1" Type="` + document + `/officeDocument" Target="xl/workbook.xml"/></Relationships>`)},
		{"xl/workbook.xml", []byte(head + `<workbook ` + main + ` xmlns:r="` + document + `">` +
			`<bookViews><workbookView/></bookViews>` +
			`<sheets><sheet name="` + xmlText(sheetName(sheet)) + `" sheetId="1" r:id="rId1"/></sheets></workbook>`)},
		{"xl/_rels/workbook.xml.rels", []byte(head + `<Relationships ` + relationships + `>` +
			`<Relationship Id="rId1" Type="` + document + `/worksheet" Target="worksheets/sheet1.xml"/>` +
			`<Relationship Id="rId2" Type="` + document + `/styles" Target="styles.xml"/></Relationships>`)},
		{"xl/styles.xml", styles.Bytes()},
		{"xl/worksheets/sheet1.xml", page.Bytes()},
	}
	file := zip.NewWriter(w)
	for _, part := range parts {
		// The same answer makes the same file, byte for byte: a part
		// carries no moment of its own.
		to, err := file.CreateHeader(&zip.FileHeader{Name: part.name, Method: zip.Deflate, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			return err
		}
		if _, err := to.Write(part.text); err != nil {
			return err
		}
	}
	return file.Close()
}

// columnName is the name of a column of a sheet: A, B, … Z, AA, AB, …
func columnName(i int) string {
	name := ""
	for i++; i > 0; i = (i - 1) / 26 {
		name = string(rune('A'+(i-1)%26)) + name
	}
	return name
}

// sheetName is a name a sheet may have: at most 31 characters, and
// none of the seven a name may not hold.
func sheetName(name string) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`[]:*?/\`, r) || r < ' ' {
			return -1
		}
		return r
	}, name)
	name = strings.Trim(name, "' ")
	if runes := []rune(name); len(runes) > 31 {
		name = string(runes[:31])
	}
	if name == "" {
		return "Sheet1"
	}
	return name
}

// xmlText writes a text into XML: the five marks of XML escaped, and a
// character XML cannot hold written the way an Excel file writes one.
func xmlText(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\'':
			b.WriteString("&apos;")
		case r == '\t' || r == '\n':
			b.WriteRune(r)
		case r < ' ' || r == 0xFFFE || r == 0xFFFF || r == utf8.RuneError:
			fmt.Fprintf(&b, "_x%04X_", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
