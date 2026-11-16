package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
)

// The case of ADR-0065, item 1: Acme's bank tells Acme that an invoice
// was paid. The class, the inlet and the message are the record's own.
const (
	bankInvoice = `{
  "name": "Invoice",
  "variables": {
    "customer": {"type": "text", "required": true},
    "total":    {"type": "money", "required": true},
    "paid_on":  {"type": "date"},
    "bank_ref": {"type": "text"}
  },
  "states": {"initial": "sent", "list": ["sent", "paid"]},
  "transitions": [
    {"name": "pay", "from": "sent", "to": "paid"}
  ],
  "access": {
    "read":   "INGROUP(\"finance\") OR INGROUP(\"payments\")",
    "create": "INGROUP(\"finance\")",
    "update": "INGROUP(\"finance\")",
    "delete": "FALSE",
    "transition": {"pay": "INGROUP(\"payments\")"}
  }
}`
	bankPaid = `{
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
	bankMessage = `{"id": "ntc_8841", "type": "payment.received",
 "payment": {"reference": "FV/2026/11/0043", "amount": "2400.00",
             "currency": "PLN", "booked": "2026-11-16"}}`
	bankSecret = "whsec_the_bank_and_acme_share_this"
)

// The people of the case: Olga keeps the database and designs in it,
// Dan designs and administers nothing, Ann is in finance, and bank-in is
// the service the inlet acts as.
var (
	olgaBoth = As(Principal{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators, GroupDesigners}})
	annFin   = As(Principal{ID: "ann", Kind: ActorPerson, Groups: []string{"finance"}})
)

// secretsOfTheBank supplies the one secret of the case.
func secretsOfTheBank(_ context.Context, connection, name string) (string, error) {
	if connection == "bank" && name == "signing_key" {
		return bankSecret, nil
	}
	return "", errors.New("no secret " + name + " of " + connection)
}

// bankDatabase opens a database that keeps users and takes messages,
// with the people, the class and two invoices of the case.
func bankDatabase(t *testing.T, o Options) *Store {
	t.Helper()
	ctx := context.Background()
	o.Users = true
	s, _ := openStore(t, o)
	for _, u := range []User{
		{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators, GroupDesigners}},
		{ID: "dan", Kind: ActorPerson, System: []string{GroupDesigners}},
		{ID: "ann", Kind: ActorPerson, Groups: []string{"finance"}},
		{ID: "bank-in", Kind: ActorService, Groups: []string{"payments"}},
	} {
		if _, err := s.CreateUser(ctx, u, machine); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DefineClass(ctx, []byte(bankInvoice), olgaBoth); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"FV/2026/11/0043", "FV/2026/11/0044"} {
		if _, err := s.Create(ctx, "Invoice", Values{"customer": "Globex", "total": "2400.00 PLN"}, annFin, WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// signed is a message of the bank with its signature, made at a moment.
func signed(body string, at time.Time) *Message {
	ts := strconv.FormatInt(at.Unix(), 10)
	return &Message{Headers: map[string]string{"Bank-Signature": "t=" + ts + ",v1=" + Sign(bankSecret, ts, []byte(body)), "Content-Type": "application/json"}, Body: []byte(body)}
}

// The case of the record, from the definition to the invoice that is
// paid once however often the bank says so.
func TestInlet_TheCaseOfTheRecord(t *testing.T) {
	ctx := context.Background()
	s := bankDatabase(t, Options{Inbound: true, Secrets: secretsOfTheBank})

	// A designer writes the inlet, has the engine check it and tries it
	// with a message; an administrator defines it (item 6).
	if diags := s.ValidateInlet([]byte(bankPaid)); len(diags) > 0 {
		t.Fatalf("the record's inlet: %s", diags.Error())
	}
	trial, err := s.TryInlet(ctx, "", []byte(bankPaid), &Message{Body: []byte(bankMessage)}, danUser)
	if err != nil {
		t.Fatal(err)
	}
	if !trial.Acts || trial.Inlet != "bank.paid" || trial.As != "bank-in" || trial.Command != "transition" ||
		trial.Object["class"] != "Invoice" || trial.Object["id"] != "FV/2026/11/0043" || trial.Object["transition"] != "pay" ||
		trial.Object["key"] != "in:bank.paid:ntc_8841" {
		t.Errorf("the trial: %+v", trial)
	}
	if values, _ := trial.Object["values"].(Values); values["paid_on"] != "2026-11-16" || values["bank_ref"] != "ntc_8841" {
		t.Errorf("the values of the trial: %+v", trial.Object["values"])
	}
	if _, err := s.TryInlet(ctx, "", []byte(bankPaid), &Message{Body: []byte(bankMessage)}, annFin); code(err) != CodeForbidden {
		t.Errorf("a trial by somebody who designs nothing: %v", err)
	}
	e := refusedWith(t, "an inlet defined by a designer", second(s.DefineInlet(ctx, []byte(bankPaid), danUser)), CodeForbidden)
	if e.Path != "/system/"+GroupAdministrators {
		t.Errorf("the refusal names %s", e.Path)
	}
	info, err := s.DefineInlet(ctx, []byte(bankPaid), olgaBoth)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "bank.paid" || info.Version != 1 || info.Address != "/v1/in/bank.paid" || info.As != "bank-in" || !info.Available || info.Why != "" ||
		info.Proof.Form != "timestamp.body" || info.Proof.Header != "Bank-Signature" || info.Command != "transition" || info.Class != "Invoice" || info.Transition != "pay" ||
		info.Defined == nil || info.Defined.By != "olga" {
		t.Errorf("the inlet as defined: %+v", info)
	}
	// The same definition again is no new version.
	if again, err := s.DefineInlet(ctx, []byte(bankPaid), olgaBoth); err != nil || again.Version != 1 {
		t.Errorf("the same definition again: %+v %v", again, err)
	}
	if list := s.Inlets(); len(list) != 1 || list[0].Name != "bank.paid" {
		t.Errorf("the inlets: %+v", list)
	}

	// The bank's message: the invoice is paid, by bank-in, with the day
	// and the bank's number.
	now := s.now()
	receipt, err := s.Receive(ctx, "bank.paid", signed(bankMessage, now))
	if err != nil {
		t.Fatal(err)
	}
	doc := receipt.Document
	if receipt.Inlet != "bank.paid" || receipt.As != "bank-in" || receipt.Ignored || receipt.Key != "in:bank.paid:ntc_8841" || doc == nil ||
		doc.State != "paid" || doc.Version != 2 || doc.UpdatedBy != "bank-in" || doc.Values["paid_on"] != "2026-11-16" || doc.Values["bank_ref"] != "ntc_8841" {
		t.Fatalf("the receipt: %+v %+v", receipt, doc)
	}
	// The same message again makes no second commit, and is answered as
	// the first was.
	for i := 0; i < 2; i++ {
		again, err := s.Receive(ctx, "BANK.PAID", signed(bankMessage, now.Add(time.Minute)))
		if err != nil || again.Document == nil || again.Document.Version != 2 || again.Document.State != "paid" {
			t.Fatalf("the same message again: %+v %v", again, err)
		}
	}
	events, err := s.Events(ctx, "Invoice", "FV/2026/11/0043", annFin)
	if err != nil || len(events) != 2 || events[1].Kind != EventTransition || events[1].ActorID != "bank-in" || events[1].ActorKind != ActorService || events[1].CommandKey != "in:bank.paid:ntc_8841" {
		t.Errorf("the events of the invoice: %+v %v", events, err)
	}

	// A message whose proof does not hold is not read: another secret,
	// a body that was changed, no signature, and a signature that is
	// too old. Each is answered the same.
	forged := signed(bankMessage, now)
	forged.Headers["Bank-Signature"] = "t=" + strconv.FormatInt(now.Unix(), 10) + ",v1=" + Sign("another secret", strconv.FormatInt(now.Unix(), 10), []byte(bankMessage))
	changed := signed(bankMessage, now)
	changed.Body = []byte(strings.Replace(bankMessage, "0043", "0044", 1))
	bare := &Message{Body: []byte(strings.Replace(bankMessage, "0043", "0044", 1))}
	for what, m := range map[string]*Message{"another secret": forged, "a changed body": changed, "no signature": bare,
		"too old": signed(strings.Replace(bankMessage, "0043", "0044", 1), now.Add(-6*time.Minute)), "too new": signed(strings.Replace(bankMessage, "0043", "0044", 1), now.Add(6*time.Minute))} {
		receipt, err := s.Receive(ctx, "bank.paid", m)
		if !errors.Is(err, ErrNotProven) || receipt == nil || receipt.As != "bank-in" || receipt.Document != nil {
			t.Errorf("%s: %+v %v", what, receipt, err)
		}
	}
	if other, _ := s.Get(ctx, "Invoice", "FV/2026/11/0044", annFin); other.State != "sent" || other.Version != 1 {
		t.Errorf("a message without a proof moved the other invoice: %+v", other)
	}

	// A message the inlet does not act on is answered and does nothing.
	if receipt, err := s.Receive(ctx, "bank.paid", signed(strings.Replace(strings.Replace(bankMessage, "payment.received", "payment.returned", 1), "0043", "0044", 1), now)); err != nil || !receipt.Ignored || receipt.Document != nil || receipt.Key != "" {
		t.Errorf("a message of another kind: %+v %v", receipt, err)
	}
	// The database refuses what it would refuse bank-in with a key of
	// its own: an invoice that is paid already, under another number of
	// the bank, and one that is not there.
	if _, err := s.Receive(ctx, "bank.paid", signed(strings.Replace(bankMessage, "ntc_8841", "ntc_8842", 1), now)); code(err) != CodeWrongState {
		t.Errorf("a second notice for a paid invoice: %v", err)
	}
	if _, err := s.Receive(ctx, "bank.paid", signed(strings.Replace(strings.Replace(bankMessage, "ntc_8841", "ntc_8843", 1), "0043", "0099", 1), now)); code(err) != CodeUnknownDocument {
		t.Errorf("a notice for an invoice that is not there: %v", err)
	}
	// The number of a message names one message: the same number with
	// another invoice in it is not the same message, and is refused.
	if _, err := s.Receive(ctx, "bank.paid", signed(strings.Replace(bankMessage, "0043", "0044", 1), now)); code(err) != CodeKeyReused {
		t.Errorf("another message under the number of the first: %v", err)
	}
	// A message the inlet cannot read: a day that is none, no number of
	// the bank, a body that is no JSON, and a key that is too long.
	for what, tc := range map[string]struct{ body, path string }{
		"a day that is none": {strings.Replace(strings.Replace(bankMessage, "2026-11-16", "16.11.2026", 1), "0043", "0044", 1), "/object/values/paid_on"},
		"no number":          {`{"type": "payment.received", "payment": {"reference": "FV/2026/11/0044", "booked": "2026-11-16"}}`, "/key"},
		"no reference":       {`{"id": "ntc_9", "type": "payment.received", "payment": {"booked": "2026-11-16"}}`, "/object/id"},
		"a long key":         {strings.Replace(strings.Replace(bankMessage, "ntc_8841", strings.Repeat("k", MaxMessageKey+1), 1), "0043", "0044", 1), "/key"},
	} {
		_, err := s.Receive(ctx, "bank.paid", signed(tc.body, now))
		if e := refusedWith(t, what, err, CodeBadMessage); e.Path != tc.path {
			t.Errorf("%s: the refusal names %q, want %q: %v", what, e.Path, tc.path, err)
		}
	}
	// A body that is no JSON gives the formulas a blank: when is not
	// TRUE, and the message is ignored.
	if receipt, err := s.Receive(ctx, "bank.paid", signed("hello", now)); err != nil || !receipt.Ignored {
		t.Errorf("a message that is no JSON: %+v %v", receipt, err)
	}
	if other, _ := s.Get(ctx, "Invoice", "FV/2026/11/0044", annFin); other.State != "sent" || other.Version != 1 {
		t.Errorf("a refused message moved the other invoice: %+v", other)
	}

	// Nobody takes the key of a message before the message comes: the
	// prefix of the inlets' keys is not a caller's.
	if _, err := s.Set(ctx, "Invoice", "FV/2026/11/0044", Values{"customer": "Initech"}, annFin, Key("in:bank.paid:ntc_9000")); code(err) != CodeBadValue || !strings.Contains(err.Error(), "in:") {
		t.Errorf("a caller's key with the prefix of the inlets: %v", err)
	}

	// A name that is no inlet.
	if receipt, err := s.Receive(ctx, "bank.refunded", signed(bankMessage, now)); code(err) != CodeUnknownInlet || receipt != nil {
		t.Errorf("a name that is no inlet: %+v %v", receipt, err)
	}
	if _, err := s.TryInlet(ctx, "bank.refunded", nil, nil, danUser); code(err) != CodeUnknownInlet {
		t.Errorf("a trial of a name that is no inlet: %v", err)
	}
	if _, err := s.TryInlet(ctx, "bank.paid", []byte(bankPaid), nil, danUser); code(err) != CodeBadValue {
		t.Errorf("a trial of a name and a definition: %v", err)
	}
	// A trial of the stored inlet, with a message it does not act on.
	if trial, err := s.TryInlet(ctx, "bank.paid", nil, &Message{Body: []byte(`{"type": "other"}`)}, danUser); err != nil || trial.Acts || trial.Command != "" || trial.Object != nil {
		t.Errorf("a trial of a message of another kind: %+v %v", trial, err)
	}

	// The inlet acts as a user: when Olga disables bank-in, it acts as
	// nobody, and says so to whoever lists it.
	off := true
	if _, err := s.SetUser(ctx, "bank-in", UserEdit{Disabled: &off}, olgaBoth); err != nil {
		t.Fatal(err)
	}
	receipt, err = s.Receive(ctx, "bank.paid", signed(strings.Replace(strings.Replace(bankMessage, "ntc_8841", "ntc_8850", 1), "0043", "0044", 1), now))
	if e := refusedWith(t, "a message for a disabled user", err, CodeUnknownInlet); !strings.Contains(e.Message, "disabled") || receipt == nil {
		t.Errorf("a message for a disabled user: %+v %v", receipt, err)
	}
	if info, _ := s.Inlet("bank.paid"); info == nil || info.Available || !strings.Contains(info.Why, "disabled") {
		t.Errorf("the inlet of a disabled user: %+v", info)
	}
	on := false
	if _, err := s.SetUser(ctx, "bank-in", UserEdit{Disabled: &on}, olgaBoth); err != nil {
		t.Fatal(err)
	}

	// Removing it closes the address; its versions stay, and defining
	// it again is its next version.
	if err := s.RemoveInlet(ctx, "bank.paid", danUser); code(err) != CodeForbidden {
		t.Errorf("an inlet removed by a designer: %v", err)
	}
	if err := s.RemoveInlet(ctx, "bank.paid", olgaBoth); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receive(ctx, "bank.paid", signed(strings.Replace(strings.Replace(bankMessage, "ntc_8841", "ntc_8850", 1), "0043", "0044", 1), now)); code(err) != CodeUnknownInlet {
		t.Errorf("a message for a removed inlet: %v", err)
	}
	if err := s.RemoveInlet(ctx, "bank.paid", olgaBoth); code(err) != CodeUnknownInlet {
		t.Errorf("removing it twice: %v", err)
	}
	if list := s.Inlets(); len(list) != 0 {
		t.Errorf("the inlets after the removal: %+v", list)
	}
	if stored, err := s.Definition(ctx, DefinitionInlet, "bank.paid", 0); err != nil || stored.Version != 1 || stored.Defined == nil || stored.Defined.By != "olga" || !strings.Contains(string(stored.Definition), `"bank-in"`) {
		t.Errorf("the definition of a removed inlet: %+v %v", stored, err)
	}
	if info, err := s.DefineInlet(ctx, []byte(bankPaid), olgaBoth); err != nil || info.Version != 2 {
		t.Fatalf("defined again: %+v %v", info, err)
	}
	if receipt, err := s.Receive(ctx, "bank.paid", signed(strings.Replace(strings.Replace(bankMessage, "ntc_8841", "ntc_8850", 1), "0043", "0044", 1), now)); err != nil || receipt.Document.State != "paid" {
		t.Errorf("a message for the inlet defined again: %+v %v", receipt, err)
	}
	if list, err := s.Definitions(ctx, DefinitionInlet, "bank.paid"); err != nil || len(list) != 2 || list[1].Version != 2 || list[1].By != "olga" {
		t.Errorf("who defined the inlet: %+v %v", list, err)
	}
	if stored, err := s.Definition(ctx, DefinitionInlet, "bank.paid", 1); err != nil || stored.Version != 1 {
		t.Errorf("the first version of the inlet: %+v %v", stored, err)
	}
	if _, err := s.Definition(ctx, DefinitionInlet, "bank.paid", 7); code(err) != CodeUnknownInlet {
		t.Errorf("a version the inlet has not: %v", err)
	}
}

// second is the error of a call that answers two things.
func second[T any](_ T, err error) error { return err }

// An inlet is kept in the database: a program that opens it again has
// it, with who defined it, and takes its messages if it takes any.
func TestInlet_Kept(t *testing.T) {
	ctx := context.Background()
	o := Options{Inbound: true, Secrets: secretsOfTheBank, Users: true}
	s, path := openStore(t, o)
	for _, u := range []User{{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators, GroupDesigners}}, {ID: "ann", Kind: ActorPerson, Groups: []string{"finance"}},
		{ID: "bank-in", Kind: ActorService, Groups: []string{"payments"}}} {
		if _, err := s.CreateUser(ctx, u, machine); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DefineClass(ctx, []byte(bankInvoice), olgaBoth); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Invoice", Values{"customer": "Globex", "total": "2400.00 PLN"}, annFin, WithID("FV/2026/11/0043")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineInlet(ctx, []byte(bankPaid), olgaBoth); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// A program that takes no messages has the inlet, says why it does
	// not act on it, takes no definition of one and answers a message
	// as one for a name that is no inlet.
	quiet, err := Open(path, Options{Users: true, Now: o.Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	if info, ok := quiet.Inlet("bank.paid"); !ok || info.Available || !strings.Contains(info.Why, "takes no messages") || info.Defined == nil || info.Defined.By != "olga" {
		t.Errorf("the inlet in a program that takes no messages: %+v", info)
	}
	diags := quiet.ValidateInlet([]byte(strings.Replace(bankPaid, "bank.paid", "bank.booked", 1)))
	if !diags.Has(model.CodeNotSupported) || len(diags) != 2 {
		t.Errorf("an inlet in a program that takes no messages and supplies no secrets: %s", diags.Error())
	}
	if _, err := quiet.DefineInlet(ctx, []byte(strings.Replace(bankPaid, "bank.paid", "bank.booked", 1)), olgaBoth); err == nil {
		t.Error("a program that takes no messages took an inlet")
	}
	if _, err := quiet.Receive(ctx, "bank.paid", signed(bankMessage, quiet.now())); code(err) != CodeUnknownInlet {
		t.Errorf("a message in a program that takes no messages: %v", err)
	}
	quiet.Close()

	// One that takes messages and supplies no secrets cannot prove one.
	deaf, err := Open(path, Options{Users: true, Inbound: true, Now: o.Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deaf.Receive(ctx, "bank.paid", signed(bankMessage, deaf.now())); code(err) != CodeSecretUnavailable {
		t.Errorf("a message in a program that supplies no secrets: %v", err)
	}
	deaf.Close()
	// And one whose secret is not there says which.
	none, err := Open(path, Options{Users: true, Inbound: true, Now: o.Now, Outbox: OutboxOptions{Manual: true},
		Secrets: func(context.Context, string, string) (string, error) { return "", errors.New("it is in neither place") }})
	if err != nil {
		t.Fatal(err)
	}
	_, err = none.Receive(ctx, "bank.paid", signed(bankMessage, none.now()))
	if e := refusedWith(t, "a message whose secret is not there", err, CodeSecretUnavailable); !strings.Contains(e.Message, "signing_key") || !strings.Contains(e.Message, "neither place") || strings.Contains(e.Message, bankSecret) {
		t.Errorf("a message whose secret is not there: %v", err)
	}
	none.Close()

	again, err := Open(path, Options{Users: true, Inbound: true, Secrets: secretsOfTheBank, Now: o.Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if receipt, err := again.Receive(ctx, "bank.paid", signed(bankMessage, again.now())); err != nil || receipt.Document.State != "paid" {
		t.Errorf("a message after the database was opened again: %+v %v", receipt, err)
	}
	// A database without users of its own keeps no inlet: there is
	// nobody for one to act as.
	plain, _ := openStore(t, Options{Inbound: true, Secrets: secretsOfTheBank})
	if _, err := plain.DefineClass(ctx, []byte(bankInvoice), olgaBoth); err != nil {
		t.Fatal(err)
	}
	if diags := plain.ValidateInlet([]byte(bankPaid)); !diags.Has(model.CodeNotSupported) {
		t.Errorf("an inlet in a database that keeps no users: %s", diags.Error())
	}
	if _, err := plain.DefineInlet(ctx, []byte(bankPaid), olgaBoth); code(err) != CodeNotSupported {
		t.Errorf("an inlet defined in a database that keeps no users: %v", err)
	}
}

// What an inlet is checked against in the database: the user it names,
// the stored inlet of its name, and the names its tables take.
func TestInlet_CheckedAgainstTheDatabase(t *testing.T) {
	ctx := context.Background()
	s := bankDatabase(t, Options{Inbound: true, Secrets: secretsOfTheBank})
	cases := map[string]struct {
		old, new   string
		code, path string
	}{
		"a user there is not":       {`"as": "bank-in"`, `"as": "bank"`, CodeUnknownUser, "/as"},
		"a person":                  {`"as": "bank-in"`, `"as": "ann"`, model.CodeNotAllowed, "/as"},
		"a class there is not":      {`"class": "Invoice"`, `"class": "Payment"`, model.CodeUnknownClass, "/object/class"},
		"a variable there is not":   {`"bank_ref":`, `"reference":`, model.CodeUnknownVariable, "/object/values/reference"},
		"a transition there is not": {`"transition": "pay"`, `"transition": "settle"`, model.CodeUnknownTransition, "/object/transition"},
	}
	for what, tc := range cases {
		diags := s.ValidateInlet([]byte(strings.Replace(bankPaid, tc.old, tc.new, 1)))
		if len(diags) != 1 || diags[0].Code != tc.code || diags[0].Path != tc.path {
			t.Errorf("%s: %s", what, diags.Error())
		}
		var refused model.Diagnostics
		if _, err := s.DefineInlet(ctx, []byte(strings.Replace(bankPaid, tc.old, tc.new, 1)), olgaBoth); !errors.As(err, &refused) || !refused.Has(tc.code) {
			t.Errorf("%s, defined: %v", what, err)
		}
	}
	if list := s.Inlets(); len(list) != 0 {
		t.Errorf("a refused inlet was kept: %+v", list)
	}
	if _, err := s.DefineInlet(ctx, []byte(bankPaid), olgaBoth); err != nil {
		t.Fatal(err)
	}
	// A redefinition is the next version, whatever it says its version
	// is; a name that differs in case only is the same inlet.
	changed := strings.Replace(bankPaid, `"label": "The bank says that an invoice was paid"`, `"label": "Paid, says the bank"`, 1)
	if diags := s.ValidateInlet([]byte(strings.Replace(changed, `"name": "bank.paid",`, `"name": "bank.paid", "version": 5,`, 1))); len(diags) != 1 || diags[0].Path != "/version" {
		t.Errorf("a version that is not the next: %s", diags.Error())
	}
	if diags := s.ValidateInlet([]byte(strings.Replace(changed, `"bank.paid"`, `"Bank.Paid"`, 1))); len(diags) != 1 || diags[0].Code != model.CodeDuplicate {
		t.Errorf("a name that differs in case only: %s", diags.Error())
	}
	if info, err := s.DefineInlet(ctx, []byte(changed), olgaBoth); err != nil || info.Version != 2 || info.Label != "Paid, says the bank" {
		t.Errorf("a redefinition: %+v %v", info, err)
	}
	// The tables of the inlets are the engine's: no class takes their
	// names.
	for _, name := range []string{"inlets", "Inlet_Versions"} {
		_, err := s.DefineClass(ctx, []byte(`{"name": "`+name+`", "variables": {"a": {"type": "text"}}, "states": {"initial": "new", "list": ["new"]}}`), olgaBoth)
		var refused model.Diagnostics
		if !errors.As(err, &refused) || !refused.Has(model.CodeReservedName) {
			t.Errorf("a class named %s: %v", name, err)
		}
	}
	// A class that changes under an inlet does not stop the database
	// from opening: what the inlet says of it is found when a message
	// comes.
	if _, err := s.DefineClass(ctx, []byte(strings.Replace(bankInvoice, `"bank_ref": {"type": "text"}`, `"bank_number": {"type": "text"}`, 1)), olgaBoth); err != nil {
		t.Fatal(err)
	}
	_, err := s.Receive(ctx, "bank.paid", signed(bankMessage, s.now()))
	if code(err) != CodeUnknownVariable {
		t.Errorf("a message for a variable the class dropped: %v", err)
	}
}

// The three forms of a proof (ADR-0065, item 3), each checked with the
// secret and the header as the sender wrote it.
func TestInlet_Proofs(t *testing.T) {
	body := []byte(`{"id": 1}`)
	mac := hmac.New(sha256.New, []byte(bankSecret))
	mac.Write(body)
	sum := mac.Sum(nil)
	at := time.Unix(1794844920, 0)
	ts := strconv.FormatInt(at.Unix(), 10)
	cases := []struct {
		what   string
		proof  model.Proof
		header string
		value  string
		holds  bool
	}{
		{"the secret shown", model.Proof{Form: model.ProofShown, Header: "x-token"}, "X-Token", bankSecret, true},
		{"another secret shown", model.Proof{Form: model.ProofShown, Header: "x-token"}, "X-Token", bankSecret + "x", false},
		{"the beginning of the secret", model.Proof{Form: model.ProofShown, Header: "x-token"}, "X-Token", bankSecret[:10], false},
		{"nothing shown", model.Proof{Form: model.ProofShown, Header: "x-token"}, "X-Token", "", false},
		{"the secret after its prefix", model.Proof{Form: model.ProofShown, Header: "authorization", Prefix: "Bearer "}, "Authorization", "Bearer " + bankSecret, true},
		{"the secret without its prefix", model.Proof{Form: model.ProofShown, Header: "authorization", Prefix: "Bearer "}, "Authorization", bankSecret, false},
		{"the secret in another header", model.Proof{Form: model.ProofShown, Header: "x-token"}, "X-Other", bankSecret, false},
		{"the body signed, in hex", model.Proof{Form: model.ProofBody, Header: "x-signature", Encoding: model.EncodingHex}, "X-Signature", hex.EncodeToString(sum), true},
		{"the body signed, in capitals", model.Proof{Form: model.ProofBody, Header: "x-signature", Encoding: model.EncodingHex}, "x-signature", strings.ToUpper(hex.EncodeToString(sum)), true},
		{"the body signed, after a prefix", model.Proof{Form: model.ProofBody, Header: "x-hub-signature-256", Prefix: "sha256=", Encoding: model.EncodingHex}, "X-Hub-Signature-256", "sha256=" + hex.EncodeToString(sum), true},
		{"the body signed, in base64", model.Proof{Form: model.ProofBody, Header: "x-hmac", Encoding: model.EncodingBase64}, "X-Hmac", base64.StdEncoding.EncodeToString(sum), true},
		{"hex where base64 is read", model.Proof{Form: model.ProofBody, Header: "x-hmac", Encoding: model.EncodingBase64}, "X-Hmac", hex.EncodeToString(sum), false},
		{"another body signed", model.Proof{Form: model.ProofBody, Header: "x-signature", Encoding: model.EncodingHex}, "X-Signature", hex.EncodeToString(sha256.New().Sum(nil)), false},
		{"a timestamp and the body", model.Proof{Form: model.ProofTimestampBody, Header: "fadodb-signature"}, "FadoDB-Signature", "t=" + ts + ",v1=" + Sign(bankSecret, ts, body), true},
		{"a timestamp and another body", model.Proof{Form: model.ProofTimestampBody, Header: "fadodb-signature"}, "FadoDB-Signature", "t=" + ts + ",v1=" + Sign(bankSecret, ts, []byte(`{"id": 2}`)), false},
		{"a signature of the body alone", model.Proof{Form: model.ProofTimestampBody, Header: "fadodb-signature"}, "FadoDB-Signature", hex.EncodeToString(sum), false},
	}
	for _, tc := range cases {
		tc.proof.Secret = "signing_key"
		m := &Message{Headers: map[string]string{tc.header: tc.value}, Body: body}
		if got := proven(tc.proof, bankSecret, m, at); got != tc.holds {
			t.Errorf("%s: the proof holds %v, want %v", tc.what, got, tc.holds)
		}
	}
}

// A message that makes a document, sent as a form, with a secret shown:
// the formulas read the fields of the form, the headers and the
// parameters of the address, and never the header that holds the secret.
func TestInlet_AFormThatMakesADocument(t *testing.T) {
	ctx := context.Background()
	s := bankDatabase(t, Options{Inbound: true, Secrets: func(_ context.Context, connection, name string) (string, error) {
		return "the token of the shop", nil
	}})
	if _, err := s.CreateUser(ctx, User{ID: "shop-in", Kind: ActorService, Groups: []string{"orders"}}, machine); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(`{
  "name": "Order",
  "variables": {
    "item":   {"type": "text", "required": true},
    "count":  {"type": "number"},
    "source": {"type": "text"},
    "shop":   {"type": "text"},
    "token":  {"type": "text"},
    "whole":  {"type": "text"}
  },
  "states": {"initial": "new", "list": ["new"]},
  "access": {"read": "TRUE", "create": "INGROUP(\"orders\")", "update": "FALSE", "delete": "FALSE"}
}`), olgaBoth); err != nil {
		t.Fatal(err)
	}
	const inlet = `{
  "name": "shop.order",
  "as": "shop-in",
  "connection": {"name": "shop", "secrets": ["token"]},
  "proof": {"form": "shown", "header": "Authorization", "secret": "token", "prefix": "Bearer "},
  "command": "create",
  "object": {"class": "Order", "id": "JSONVALUE(BODY, \"number\")", "values": {
    "item": "JSONVALUE(BODY, \"item\")",
    "count": "VALUE(JSONVALUE(BODY, \"count\"))",
    "source": "JSONVALUE(PARAMS, \"source\")",
    "shop": "JSONVALUE(HEADERS, \"x-shop\")",
    "token": "JSONVALUE(HEADERS, \"authorization\")",
    "whole": "RAW"
  }}
}`
	if _, err := s.DefineInlet(ctx, []byte(inlet), olgaBoth); err != nil {
		t.Fatal(err)
	}
	message := func(body string) *Message {
		return &Message{Headers: map[string]string{"authorization": "Bearer the token of the shop", "X-Shop": "Warsaw"}, Params: map[string]string{"source": "web"}, Body: []byte(body), Form: true}
	}
	receipt, err := s.Receive(ctx, "shop.order", message("number=ORD-7&item=a+lamp&count=3"))
	if err != nil {
		t.Fatal(err)
	}
	doc := receipt.Document
	if doc.ID != "ORD-7" || doc.CreatedBy != "shop-in" || doc.Values["item"] != "a lamp" || doc.Values["count"] != float64(3) || doc.Values["source"] != "web" || doc.Values["shop"] != "Warsaw" ||
		doc.Values["whole"] != "number=ORD-7&item=a+lamp&count=3" {
		t.Errorf("the order: %+v", doc)
	}
	// The header that holds the secret is not among the headers a
	// formula reads: the secret reaches no document.
	if doc.Values["token"] != nil {
		t.Errorf("the secret of the proof reached a document: %v", doc.Values["token"])
	}
	// Without a key of its own, the same bytes are the same message.
	sum := sha256.Sum256([]byte("number=ORD-7&item=a+lamp&count=3"))
	if receipt.Key != "in:shop.order:"+hex.EncodeToString(sum[:]) {
		t.Errorf("the key of a message that names none: %s", receipt.Key)
	}
	if again, err := s.Receive(ctx, "shop.order", message("number=ORD-7&item=a+lamp&count=3")); err != nil || again.Document.Version != 1 {
		t.Errorf("the same form again: %+v %v", again, err)
	}
	// Other bytes are another message: the same number is then a second
	// document of that id, which the database refuses.
	if _, err := s.Receive(ctx, "shop.order", message("number=ORD-7&item=a+lamp&count=4")); code(err) != CodeDuplicate {
		t.Errorf("another form with the same number: %v", err)
	}
	// A create that names no id is given one.
	if _, err := s.DefineInlet(ctx, []byte(strings.Replace(strings.Replace(inlet, `"id": "JSONVALUE(BODY, \"number\")", `, ``, 1), "shop.order", "shop.anything", 1)), olgaBoth); err != nil {
		t.Fatal(err)
	}
	if receipt, err := s.Receive(ctx, "shop.anything", message("item=a+chair")); err != nil || receipt.Document.ID == "" || receipt.Document.Values["item"] != "a chair" {
		t.Errorf("a create without an id: %+v %v", receipt, err)
	}
	// What the user of the inlet may not do, the inlet may not: bank-in
	// creates no invoice, whoever sends the message.
	if _, err := s.DefineInlet(ctx, []byte(`{"name": "bank.invoice", "as": "bank-in", "connection": {"name": "shop", "secrets": ["token"]},
 "proof": {"form": "shown", "header": "Authorization", "secret": "token", "prefix": "Bearer "},
 "command": "create", "object": {"class": "Invoice", "values": {"customer": "RAW", "total": "MONEY(1, \"PLN\")"}}}`), olgaBoth); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receive(ctx, "bank.invoice", message("anybody")); code(err) != CodeForbidden {
		t.Errorf("an invoice made by the bank's user: %v", err)
	}
}
