package model

import (
	"strings"
	"testing"
)

// A language is a tag of BCP 47 by its shape, written in its canonical
// case; the registry is not asked.
func TestWords_LanguageTags(t *testing.T) {
	for tag, want := range map[string]string{
		"pl": "pl", "de": "de", "pt-BR": "pt-BR", "zh-Hant": "zh-Hant", "zh-Hant-TW": "zh-Hant-TW", "es-419": "es-419",
		"de-CH-1901": "de-CH-1901", "sl-rozaj-biske": "sl-rozaj-biske", "en-x-acme": "en-x-acme", "en-US-u-ca-gregory": "en-US-u-ca-gregory",
		"zh-yue-HK": "zh-yue-HK", "PT-br": "pt-BR", "ZH-hant": "zh-Hant",
	} {
		got, why := languageTag(tag)
		if why != "" || got != want {
			t.Errorf("%q reads as %q (%s), want %q", tag, got, why, want)
		}
	}
	for _, tag := range []string{"", "p", "polish", "pl_PL", "pl-", "-pl", "pl--PL", "en-x", "en-u", "en-a-b", "12", "pl-PL-x-", "eñ"} {
		if got, why := languageTag(tag); why == "" {
			t.Errorf("%q reads as the tag %q", tag, got)
		}
	}
}

// The words of the record's invoice in Polish (ADR-0068, item 6), with
// the sections of ADR-0069, check against the class; every wrong path is
// refused at its place.
func TestWords_CheckedAgainstTheClass(t *testing.T) {
	cls := mustDefine(t, strings.Replace(sectionsInvoice, `"notes":        {"type": "text",  "label": "Notes"}`,
		`"notes":        {"type": "text",  "label": "Notes"},
    "lines":        {"type": "rows", "schema": {"sku": "text", "qty": {"type": "int", "label": "Quantity"}}},
    "contact":      {"type": "object", "schema": "Person"}`, 1))
	words := func(src string) Diagnostics {
		def, diags := ParseWords([]byte(src))
		if len(diags) > 0 {
			return diags
		}
		return CheckWords(def, WordsTarget{Class: cls})
	}
	if diags := words(`{"class": "Invoice", "language": "pl", "label": "Faktura", "description": "Faktura dostawcy, od przyjęcia do zapłaty.",
	  "variables": {"number": {"label": "Numer"}, "customer": {"label": "Dostawca"}, "net": {"label": "Kwota netto", "description": "Kwota przed VAT."},
	                "lines": {"label": "Pozycje"}, "lines.qty": {"label": "Ilość"}, "LINES.SKU": {"label": "Kod"}},
	  "sections": {"supplier": {"label": "Dostawca"}, "address": {"label": "Adres", "description": "Adres z dokumentu dostawy."}},
	  "states": {"draft": {"label": "Szkic"}, "review": {"label": "Do zatwierdzenia"}, "paid": {"label": "Zapłacona"}},
	  "transitions": {"approve": {"label": "Zatwierdź"}}}`); len(diags) > 0 {
		t.Fatalf("the record's Polish words: %v", diags)
	}
	long := strings.Repeat("x", maxWords+1)
	for _, c := range []struct{ name, src, code, path string }{
		{"no language", `{"class": "Invoice"}`, CodeMissing, "/language"},
		{"a language that is no tag", `{"class": "Invoice", "language": "polish"}`, CodeBadValue, "/language"},
		{"a tag in another case", `{"class": "Invoice", "language": "pt-br"}`, CodeBadValue, "/language"},
		{"nothing translated named", `{"language": "pl"}`, CodeMissing, "/class"},
		{"two things named", `{"class": "Invoice", "table": "Rates", "language": "pl"}`, CodeBadValue, "/class"},
		{"a word the definition does not know", `{"class": "Invoice", "language": "pl", "caption": "Faktura"}`, CodeBadJSON, ""},
		{"a variable the class does not have", `{"class": "Invoice", "language": "pl", "variables": {"vat_rate": {"label": "Stawka"}}}`, CodeBadValue, "/variables/vat_rate"},
		{"a column a rows variable does not have", `{"class": "Invoice", "language": "pl", "variables": {"lines.price": {"label": "Cena"}}}`, CodeBadValue, "/variables/lines.price"},
		{"a field of an object, which its schema words", `{"class": "Invoice", "language": "pl", "variables": {"contact.email": {"label": "E-mail"}}}`, CodeBadValue, "/variables/contact.email"},
		{"a column of a variable that has none", `{"class": "Invoice", "language": "pl", "variables": {"net.amount": {"label": "Kwota"}}}`, CodeBadValue, "/variables/net.amount"},
		{"a section the class does not have", `{"class": "Invoice", "language": "pl", "sections": {"payment": {"label": "Płatność"}}}`, CodeBadValue, "/sections/payment"},
		{"a state the class does not have", `{"class": "Invoice", "language": "pl", "states": {"cancelled": {"label": "Anulowana"}}}`, CodeBadValue, "/states/cancelled"},
		{"a transition the class does not have", `{"class": "Invoice", "language": "pl", "transitions": {"pay": {"label": "Zapłać"}}}`, CodeBadValue, "/transitions/pay"},
		{"words that say nothing", `{"class": "Invoice", "language": "pl", "variables": {"net": {}}}`, CodeMissing, "/variables/net"},
		{"columns of a class", `{"class": "Invoice", "language": "pl", "columns": {"net": {"label": "Netto"}}}`, CodeNotAllowed, "/columns"},
		{"a label past the limit", `{"class": "Invoice", "language": "pl", "variables": {"net": {"label": "` + long + `"}}}`, CodeBadValue, "/variables/net/label"},
	} {
		diags := words(c.src)
		found := false
		for _, d := range diags {
			if d.Code == c.code && d.Path == c.path {
				found = true
				if d.Code != CodeBadJSON && d.Hint == "" && d.Path != "/variables/net/label" {
					t.Errorf("%s: no hint: %+v", c.name, d)
				}
			}
		}
		if !found {
			t.Errorf("%s: want %s at %s, got %v", c.name, c.code, c.path, diags)
		}
	}
	// The field of an object is named in the hint by its schema.
	if diags := words(`{"class": "Invoice", "language": "pl", "variables": {"contact.email": {"label": "E-mail"}}}`); len(diags) != 1 || !strings.Contains(diags[0].Hint, `"schema": "Person"`) {
		t.Errorf("the hint for a field of an object: %v", diags)
	}

	// Of a table, its columns; of a schema, its fields, and no label.
	tdef, _ := ParseTable([]byte(`{"name": "Rates", "columns": {"code": "text", "rate": "decimal(6)"}, "key": ["code"]}`))
	tbl, diags := CompileTable(tdef)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	def, _ := ParseWords([]byte(`{"table": "Rates", "language": "pl", "label": "Kursy", "columns": {"rate": {"label": "Kurs"}}}`))
	if diags := CheckWords(def, WordsTarget{Table: tbl}); len(diags) > 0 {
		t.Errorf("the words of a table: %v", diags)
	}
	def, _ = ParseWords([]byte(`{"table": "Rates", "language": "pl", "columns": {"day": {"label": "Dzień"}}, "variables": {"code": {"label": "Kod"}}}`))
	if diags := CheckWords(def, WordsTarget{Table: tbl}); len(diags) != 2 || !diags.Has(CodeBadValue) || !diags.Has(CodeNotAllowed) {
		t.Errorf("a column the table does not have, and variables of a table: %v", diags)
	}
	address, _ := NewSchemaSet().Lookup("Address")
	def, _ = ParseWords([]byte(`{"schema": "Address", "language": "pl", "fields": {"city": {"label": "Miasto"}, "postal_code": {"label": "Kod pocztowy"}}}`))
	if diags := CheckWords(def, WordsTarget{Schema: address}); len(diags) > 0 {
		t.Errorf("the words of the schema Address: %v", diags)
	}
	def, _ = ParseWords([]byte(`{"schema": "Address", "language": "pl", "label": "Adres", "fields": {"town": {"label": "Miasto"}}}`))
	if diags := CheckWords(def, WordsTarget{Schema: address}); len(diags) != 2 || !diags.Has(CodeNotAllowed) || !diags.Has(CodeBadValue) {
		t.Errorf("a label of a schema, and a field it does not have: %v", diags)
	}

	// Words a later version no longer uses are noted, not refused.
	def, _ = ParseWords([]byte(`{"class": "Invoice", "language": "pl", "label": "Faktura", "variables": {"net": {"label": "Netto"}, "lines.qty": {"label": "Ilość"}}, "sections": {"address": {"label": "Adres"}}}`))
	smaller := mustDefine(t, `{"name": "Invoice", "variables": {"number": {"type": "text"}}}`)
	notes := UnusedWords(def, WordsTarget{Class: smaller})
	var paths []string
	for _, n := range notes {
		if n.Code != CodeUnusedWords || !n.Note {
			t.Errorf("a note of unused words: %+v", n)
		}
		paths = append(paths, n.Path)
	}
	if got := strings.Join(paths, " "); got != "/variables/lines.qty /variables/net /sections/address" {
		t.Errorf("the unused words are at %s", got)
	}
}
