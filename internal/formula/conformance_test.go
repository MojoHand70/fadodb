package formula

import (
	"path/filepath"
	"testing"
)

// The conformance suite is the specification. Until the VM lands, this test
// proves that every case is well formed and that the parser accepts every
// evaluation formula and every accept case and refuses every reject case.

const conformanceDir = "../../conformance"

func TestConformance_EvaluationCasesParse(t *testing.T) {
	suites, err := LoadSuites(filepath.Join(conformanceDir, "formula"))
	if err != nil {
		t.Fatal(err)
	}
	if len(suites) == 0 {
		t.Fatal("no evaluation suites found")
	}
	total := 0
	for _, s := range suites {
		for _, c := range s.Cases {
			total++
			if _, err := Parse(c.Formula); err != nil {
				t.Errorf("%s: %q: %v", s.File, c.Formula, err)
			}
		}
	}
	t.Logf("%d evaluation cases in %d suites", total, len(suites))
}

func TestConformance_SyntaxCases(t *testing.T) {
	suites, err := LoadSyntaxSuites(filepath.Join(conformanceDir, "syntax"))
	if err != nil {
		t.Fatal(err)
	}
	if len(suites) == 0 {
		t.Fatal("no syntax suites found")
	}
	accepted, rejected := 0, 0
	for _, s := range suites {
		for _, f := range s.Accept {
			accepted++
			if _, err := Parse(f); err != nil {
				t.Errorf("%s: accept %q: %v", s.File, f, err)
			}
		}
		for _, r := range s.Reject {
			rejected++
			if _, err := Parse(r.Formula); err == nil {
				t.Errorf("%s: reject %q (%s): parsed without error", s.File, r.Formula, r.Reason)
			}
		}
	}
	t.Logf("%d accept and %d reject cases in %d suites", accepted, rejected, len(suites))
}

func TestConformance_LoaderRejectsMalformedSuites(t *testing.T) {
	dir := t.TempDir()
	bad := map[string]string{
		"two-outcomes.json":  `{"suite":"x","cases":[{"formula":"1","expected":1,"error":"div_by_zero"}]}`,
		"no-outcome.json":    `{"suite":"x","cases":[{"formula":"1"}]}`,
		"bad-code.json":      `{"suite":"x","cases":[{"formula":"1","error":"boom"}]}`,
		"bad-input.json":     `{"suite":"x","cases":[{"formula":"1","inputs":{"q":1},"expected":1}]}`,
		"bad-type.json":      `{"suite":"x","variables":{"q":"money"},"cases":[{"formula":"1","expected":1}]}`,
		"no-name.json":       `{"cases":[{"formula":"1","expected":1}]}`,
		"unknown-field.json": `{"suite":"x","cases":[{"formula":"1","expected":1,"note":"x"}]}`,
	}
	for name, raw := range bad {
		if err := writeFile(filepath.Join(dir, name), raw); err != nil {
			t.Fatal(err)
		}
		if _, err := loadSuite(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s: loaded without error", name)
		}
	}
	good := `{"suite":"x","variables":{"q":"number","d":"date","tags":"array<text>"},"cases":[{"formula":"q + 1","inputs":{"q":1},"expected":2},{"formula":"1 \\ 0","error":"div_by_zero"},{"formula":"BLANK","expected":null}]}`
	if err := writeFile(filepath.Join(dir, "good.json"), good); err != nil {
		t.Fatal(err)
	}
	s, err := loadSuite(filepath.Join(dir, "good.json"))
	if err != nil {
		t.Fatalf("good suite: %v", err)
	}
	if len(s.Cases) != 3 || string(s.Cases[2].Expected) != "null" || s.Cases[2].Error != "" {
		t.Errorf("good suite loaded as %+v", s)
	}
}
