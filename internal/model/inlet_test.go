package model

import (
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// The class of the record's case (ADR-0065, item 1), and one more, for a
// message that makes a document.
const (
	inletInvoice = `{
  "name": "Invoice",
  "variables": {
    "customer": {"type": "text", "required": true},
    "total":    {"type": "money", "required": true},
    "paid_on":  {"type": "date"},
    "bank_ref": {"type": "text"},
    "net":      {"formula": "AMOUNT(total)"}
  },
  "states": {"initial": "sent", "list": ["sent", "paid"]},
  "transitions": [{"name": "pay", "from": "sent", "to": "paid"}]
}`
	inletNotice = `{
  "name": "Notice",
  "variables": {
    "sender": {"type": "text", "required": true},
    "kind":   {"type": "text", "default": "\"other\""},
    "amount": {"type": "number"}
  },
  "states": {"initial": "new", "list": ["new"]}
}`
	// The inlet of the record, whole.
	inletOfTheRecord = `{
  "name": "bank.paid",
  "label": "The bank says that an invoice was paid",
  "as": "bank-in",
  "connection": {"name": "bank", "secrets": ["signing_key"]},
  "proof": {"form": "timestamp.body", "header": "Bank-Signature", "secret": "signing_key"},
  "when": "JSONVALUE(BODY, \"type\") = \"payment.received\"",
  "key": "JSONVALUE(BODY, \"id\")",
  "command": "transition",
  "object": {
    "class": "Invoice",
    "id": "JSONVALUE(BODY, \"payment.reference\")",
    "transition": "pay",
    "values": {
      "paid_on": "DATEVALUE(JSONVALUE(BODY, \"payment.booked\"))",
      "bank_ref": "JSONVALUE(BODY, \"id\")"
    }
  }
}`
)

// inletEnv is an environment with the two classes.
func inletEnv(t *testing.T) Env {
	t.Helper()
	classes := map[string]*Class{}
	for _, src := range []string{inletInvoice, inletNotice} {
		cls := mustDefine(t, src)
		classes[upper(cls.Name)] = cls
	}
	return Env{Classes: func(name string) (*Class, bool) {
		cls, ok := classes[upper(name)]
		return cls, ok
	}}
}

// An inlet (ADR-0065): the record's own compiles to a proof, a
// condition, a key and one command whose values are typed against the
// class.
func TestInlet_Define(t *testing.T) {
	env := inletEnv(t)
	in, diags := DefineInlet([]byte(inletOfTheRecord), env)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	if in.Name != "bank.paid" || in.Version != 1 || in.As != "bank-in" || in.Connection != "bank" || strings.Join(in.Secrets, ",") != "signing_key" {
		t.Errorf("the inlet: %+v", in)
	}
	if p := in.Proof; p.Form != ProofTimestampBody || p.Header != "bank-signature" || p.Secret != "signing_key" || p.Prefix != "" || p.Encoding != "" {
		t.Errorf("the proof: %+v", p)
	}
	if in.Command != InletTransition || in.Class != "Invoice" || in.Transition != "pay" || in.ID == nil || in.When == nil || in.Key == nil {
		t.Errorf("the command: %+v", in)
	}
	if len(in.Sets) != 2 || in.Sets[0].Name != "paid_on" || in.Sets[0].Variable.Type.Kind != formula.KindDate || in.Sets[1].Name != "bank_ref" {
		t.Errorf("the values: %+v", in.Sets)
	}
	// The formulas read the message and nothing else: their slots are
	// the four names.
	for _, prog := range []*formula.Program{in.When, in.Key, in.ID, in.Sets[0].Formula} {
		for _, slot := range prog.Slots {
			if slot.Name != MessageBody {
				t.Errorf("a formula of the record's inlet reads %s", slot.Name)
			}
		}
	}

	// The other two commands, the other two forms, and a class written
	// in another case than its own.
	in, diags = DefineInlet([]byte(`{"name": "shop.order", "as": "shop-in",
 "connection": {"name": "shop", "secrets": ["token"]},
 "proof": {"form": "shown", "header": "Authorization", "secret": "token", "prefix": "Bearer "},
 "command": "create",
 "object": {"class": "notice", "values": {"sender": "JSONVALUE(HEADERS, \"x-shop\")", "amount": "VALUE(JSONVALUE(PARAMS, \"amount\"))", "kind": "LEFT(RAW, 5)"}}}`), env)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	if in.Command != InletCreate || in.Class != "Notice" || in.ID != nil || in.When != nil || in.Key != nil || len(in.Sets) != 3 || in.Proof.Prefix != "Bearer " {
		t.Errorf("a create: %+v", in)
	}
	in, diags = DefineInlet([]byte(`{"name": "bank.booked", "version": 4, "as": "bank-in",
 "connection": {"name": "bank", "secrets": ["signing_key", "other"]},
 "proof": {"form": "body", "header": "X-Signature-256", "secret": "SIGNING_KEY", "prefix": "sha256=", "encoding": "base64"},
 "command": "set",
 "object": {"class": "Invoice", "id": "JSONVALUE(BODY, \"ref\")", "values": {"bank_ref": "JSONVALUE(BODY, \"id\")"}}}`), env)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	if in.Version != 4 || in.Command != InletSet || in.Proof.Secret != "signing_key" || in.Proof.Encoding != EncodingBase64 || in.Proof.Header != "x-signature-256" {
		t.Errorf("a set: %+v %+v", in, in.Proof)
	}
	// Told nothing, a signature of the body is written in hex.
	in, diags = DefineInlet([]byte(strings.Replace(inletOfTheRecord, `"form": "timestamp.body"`, `"form": "body"`, 1)), env)
	if len(diags) > 0 || in.Proof.Encoding != EncodingHex {
		t.Errorf("a signature of the body: %v %+v", diags, in)
	}
}

// What is wrong with an inlet is found when it is defined, each mistake
// with its code and its place.
func TestInlet_Diagnostics(t *testing.T) {
	env := inletEnv(t)
	// with puts one thing of the record's inlet another way.
	with := func(old, new string) string {
		if !strings.Contains(inletOfTheRecord, old) {
			t.Fatalf("the record's inlet has no %s", old)
		}
		return strings.Replace(inletOfTheRecord, old, new, 1)
	}
	const proof = `"proof": {"form": "timestamp.body", "header": "Bank-Signature", "secret": "signing_key"},`
	cases := map[string]struct {
		def        string
		code, path string
		says       string
	}{
		"not json":            {`{"name": `, CodeBadJSON, "", ""},
		"a member it has not": {with(`"label"`, `"lable"`), CodeBadJSON, "", "lable"},
		"no name":             {with(`"name": "bank.paid",`, ``), CodeMissing, "/name", ""},
		"a name of one part":  {with(`"bank.paid"`, `"paid"`), CodeBadName, "/name", "joined by a dot"},
		"a name with a slash": {with(`"bank.paid"`, `"bank/paid"`), CodeBadName, "/name", ""},
		"nobody to act as":    {with(`"as": "bank-in",`, ``), CodeMissing, "/as", "service"},
		// There is no address the whole world may write to.
		"no proof":                   {with(proof, ``), CodeMissing, "/proof", "anybody could send one"},
		"no connection":              {with(`"connection": {"name": "bank", "secrets": ["signing_key"]},`, ``), CodeMissing, "/connection", ""},
		"a form the engine has not":  {with(`"timestamp.body"`, `"oauth"`), CodeBadValue, "/proof/form", "oauth"},
		"no form":                    {with(`"form": "timestamp.body", `, ``), CodeMissing, "/proof/form", ""},
		"no header":                  {with(`"header": "Bank-Signature", `, ``), CodeMissing, "/proof/header", ""},
		"a header that is none":      {with(`"Bank-Signature"`, `"Bank Signature"`), CodeBadName, "/proof/header", ""},
		"no secret":                  {with(`, "secret": "signing_key"}`, `}`), CodeMissing, "/proof/secret", ""},
		"a secret not declared":      {with(`"secret": "signing_key"}`, `"secret": "token"}`), CodeUnknownSecret, "/proof/secret", "token"},
		"a secret declared twice":    {with(`["signing_key"]`, `["signing_key", "SIGNING_KEY"]`), CodeDuplicate, "/connection/secrets/1", ""},
		"an encoding where none is":  {with(`"secret": "signing_key"}`, `"secret": "signing_key", "encoding": "hex"}`), CodeNotAllowed, "/proof/encoding", ""},
		"a prefix where none stands": {with(`"secret": "signing_key"}`, `"secret": "signing_key", "prefix": "v1="}`), CodeNotAllowed, "/proof/prefix", ""},
		"an encoding that is none": {with(proof, `"proof": {"form": "body", "header": "X-Sig", "secret": "signing_key", "encoding": "base32"},`),
			CodeBadValue, "/proof/encoding", "base32"},
		// The formulas read the message and nothing else.
		"a condition that is none":  {with(`"JSONVALUE(BODY, \"type\") = \"payment.received\""`, `"JSONVALUE(BODY, \"type\")"`), CodeNotACondition, "/when", ""},
		"a condition that reads me": {with(`"JSONVALUE(BODY, \"type\") = \"payment.received\""`, `"USER() = \"ann\""`), CodeBadFormula, "/when", "reads the message and nothing else"},
		"a key that is a number":    {with(`"key": "JSONVALUE(BODY, \"id\")"`, `"key": "LEN(RAW)"`), CodeTypeMismatch, "/key", ""},
		"a key that reads a secret": {with(`"key": "JSONVALUE(BODY, \"id\")"`, `"key": "SECRET(\"signing_key\")"`), CodeBadFormula, "/key", "SECRET"},
		"a value that reads a document": {with(`"bank_ref": "JSONVALUE(BODY, \"id\")"`, `"bank_ref": "LOOKUP(Invoice, \"customer\", \"x\", \"customer\")"`),
			CodeBadFormula, "/object/values/bank_ref", ""},
		"a value that reads the clock":   {with(`"DATEVALUE(JSONVALUE(BODY, \"payment.booked\"))"`, `"TODAY()"`), CodeBadFormula, "/object/values/paid_on", "reads the message and nothing else"},
		"a name the message has not":     {with(`"bank_ref": "JSONVALUE(BODY, \"id\")"`, `"bank_ref": "reference"`), CodeBadFormula, "/object/values/bank_ref", ""},
		"no command":                     {with(`"command": "transition",`, ``), CodeMissing, "/command", ""},
		"a command a message is not":     {with(`"command": "transition"`, `"command": "delete"`), CodeBadValue, "/command", "delete"},
		"no object":                      {with(`"command": "transition",`, `"command": "transition"}`)[:strings.Index(with(`"command": "transition",`, `"command": "transition"}`), `"command": "transition"}`)+len(`"command": "transition"}`)], CodeMissing, "/object", ""},
		"a member the object has not":    {with(`"transition": "pay",`, `"transition": "pay", "key": "\"k\"",`), CodeBadJSON, "/object", "key"},
		"no class":                       {with(`"class": "Invoice",`, ``), CodeMissing, "/object/class", ""},
		"a class there is not":           {with(`"class": "Invoice"`, `"class": "Payment"`), CodeUnknownClass, "/object/class", "Payment"},
		"no transition":                  {with(`"transition": "pay",`, ``), CodeMissing, "/object/transition", ""},
		"a transition the class has not": {with(`"transition": "pay"`, `"transition": "settle"`), CodeUnknownTransition, "/object/transition", "settle"},
		"no document":                    {with(`"id": "JSONVALUE(BODY, \"payment.reference\")",`, ``), CodeMissing, "/object/id", ""},
		"an id that is a number":         {with(`"id": "JSONVALUE(BODY, \"payment.reference\")"`, `"id": "LEN(RAW)"`), CodeTypeMismatch, "/object/id", ""},
		"a variable the class has not":   {with(`"bank_ref":`, `"bank_reference":`), CodeUnknownVariable, "/object/values/bank_reference", ""},
		"a variable the class works out": {with(`"bank_ref":`, `"net":`), CodeNotAllowed, "/object/values/net", "computed"},
		"a text where a day goes":        {with(`"DATEVALUE(JSONVALUE(BODY, \"payment.booked\"))"`, `"JSONVALUE(BODY, \"payment.booked\")"`), CodeTypeMismatch, "/object/values/paid_on", ""},
		"a variable given twice":         {with(`"bank_ref":`, `"PAID_ON":`), CodeDuplicate, "/object/values/PAID_ON", ""},
		"a transition for another command": {`{"name": "bank.booked", "as": "bank-in", "connection": {"name": "bank", "secrets": ["k"]}, "proof": {"form": "shown", "header": "X-K", "secret": "k"},
 "command": "set", "object": {"class": "Invoice", "id": "RAW", "transition": "pay", "values": {"bank_ref": "RAW"}}}`, CodeNotAllowed, "/object/transition", ""},
		"a set of nothing": {`{"name": "bank.booked", "as": "bank-in", "connection": {"name": "bank", "secrets": ["k"]}, "proof": {"form": "shown", "header": "X-K", "secret": "k"},
 "command": "set", "object": {"class": "Invoice", "id": "RAW"}}`, CodeMissing, "/object/values", ""},
		// A create that leaves out what the class requires would refuse
		// every message.
		"a create without what the class requires": {`{"name": "shop.order", "as": "shop-in", "connection": {"name": "shop", "secrets": ["k"]}, "proof": {"form": "shown", "header": "X-K", "secret": "k"},
 "command": "create", "object": {"class": "Notice", "values": {"amount": "1"}}}`, CodeMissing, "/object/values", "sender"},
	}
	for name, tc := range cases {
		_, diags := DefineInlet([]byte(tc.def), env)
		found := false
		for _, d := range diags {
			if d.Code == tc.code && d.Path == tc.path && strings.Contains(d.Message+" "+d.Hint, tc.says) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: wanted %s at %q saying %q, got %s", name, tc.code, tc.path, tc.says, diags.Error())
		}
	}

	// A stored inlet is compiled leniently: a class the database no
	// longer has, or a variable its class dropped, is no finding when
	// the database is opened, and is found when a message comes.
	lenient := env
	lenient.Lenient = true
	for _, def := range []string{with(`"class": "Invoice"`, `"class": "Payment"`), with(`"bank_ref":`, `"bank_reference":`), with(`"transition": "pay"`, `"transition": "settle"`)} {
		if in, diags := DefineInlet([]byte(def), lenient); len(diags) > 0 || in == nil {
			t.Errorf("a stored inlet does not load: %s", diags.Error())
		}
	}
}
