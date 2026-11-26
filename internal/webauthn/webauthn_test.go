package webauthn_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/webauthn"
	"github.com/MojoHand70/fadodb/internal/webauthn/webauthntest"
)

var acme = webauthn.Party{ID: "fado.acme.example", Origin: "https://fado.acme.example"}

var ann = webauthn.User{Handle: bytes.Repeat([]byte{7}, 16), Name: "ann", Display: "Ann Nowak"}

func challenge(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

// made makes a passkey on a device and checks it.
func made(t *testing.T, d *webauthntest.Device) *webauthn.Made {
	t.Helper()
	answer, err := d.Make(acme.Origin, acme.CreationOptions(ann, challenge(1), nil, 5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	c, err := webauthn.ParseCreation([]byte(answer))
	if err != nil {
		t.Fatal(err)
	}
	m, err := acme.CheckCreation(c, challenge(1))
	if err != nil {
		t.Fatalf("a passkey of the kind %d: %v", d.Alg, err)
	}
	return m
}

// used uses the passkey a device made, as kept, and answers the check.
func used(d *webauthntest.Device, m *webauthn.Made, kept uint32) (*webauthn.Used, error) {
	answer, err := d.Use(acme.Origin, acme.RequestOptions(challenge(2), 5*time.Minute))
	if err != nil {
		return nil, err
	}
	a, err := webauthn.ParseAssertion([]byte(answer))
	if err != nil {
		return nil, err
	}
	return acme.CheckAssertion(a, challenge(2), m.Key, kept)
}

// Each kind of key the server takes is made and used: ES256 from
// Windows Hello and security keys, Ed25519, and RSA from older Windows
// Hello.
func TestEachKindOfKey(t *testing.T) {
	for _, alg := range []int{webauthntest.ES256, webauthntest.EdDSA, webauthntest.RS256} {
		d := webauthntest.New(alg)
		m := made(t, d)
		if m.Alg != alg || m.Count != 1 || !bytes.Equal(m.Credential, d.ID) {
			t.Errorf("the kind %d: made %+v", alg, m)
		}
		u, err := used(d, m, m.Count)
		if err != nil || u.Count != 2 {
			t.Errorf("the kind %d used: %+v %v", alg, u, err)
		}
	}
}

// A synced passkey, as Apple's and Google's are, keeps no counter: it
// says 0 every time, and is taken.
func TestASyncedPasskey(t *testing.T) {
	d := webauthntest.New(webauthntest.ES256)
	d.Counts, d.Synced = false, true
	m := made(t, d)
	if !m.Synced || m.Count != 0 {
		t.Fatalf("made %+v", m)
	}
	for i := 0; i < 3; i++ {
		if u, err := used(d, m, 0); err != nil || u.Count != 0 || !u.Synced {
			t.Fatalf("used %d: %+v %v", i, u, err)
		}
	}
}

// aaguid writes an AAGUID as the table of makers does.
func aaguid(t *testing.T, s string) [16]byte {
	t.Helper()
	var a [16]byte
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(b) != 16 {
		t.Fatalf("the AAGUID %q", s)
	}
	copy(a[:], b)
	return a
}

// The maker of a passkey is named by its AAGUID: Google Password
// Manager's, as Chrome says it; none for sixteen zeros, as Safari says,
// nor for one the table does not hold.
func TestTheMakerOfAPasskey(t *testing.T) {
	for _, c := range []struct{ aaguid, want string }{
		{"ea9b8d66-4d01-1d21-3ce4-b6b48cb575d4", "Google Password Manager"},
		{"fbfc3007-154e-4ecc-8c0b-6e020557d7bd", "Apple Passwords"},
		{"bada5566-a7aa-401f-bd96-45619a55120d", "1Password"},
		{"08987058-cadc-4b81-b6e1-30de50dcbe96", "Windows Hello"},
		{"00000000-0000-0000-0000-000000000000", ""},
		{"01020304-0506-0708-0102-030405060708", ""},
	} {
		d := webauthntest.New(webauthntest.ES256)
		d.Counts, d.Synced, d.AAGUID = false, true, aaguid(t, c.aaguid)
		if m := made(t, d); m.Maker != c.want {
			t.Errorf("%s: the maker %q, want %q", c.aaguid, m.Maker, c.want)
		}
	}
}

// Each AAGUID of the table of makers is written as MakerOf looks it up,
// so that none is held in a form nothing finds.
func TestTheTableOfMakers(t *testing.T) {
	n := 0
	for _, line := range strings.Split(string(makersFile(t)), "\n") {
		f := strings.SplitN(strings.TrimSpace(line), `": "`, 2)
		if len(f) != 2 || !strings.HasPrefix(f[0], `"`) {
			continue
		}
		n++
		name := strings.TrimSuffix(f[1], `",`)
		if got := webauthn.MakerOf(aaguid(t, strings.Trim(f[0], `"`))); got != name {
			t.Errorf("%s: %q, want %q", f[0], got, name)
		}
	}
	if n < 60 {
		t.Errorf("the table holds %d makers", n)
	}
}

func makersFile(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("makers.go")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The statement of a device's maker is not read, whatever its format.
func TestTheMakersStatementIsNotRead(t *testing.T) {
	d := webauthntest.New(webauthntest.ES256)
	d.Format = "packed"
	made(t, d)
}

// What the server asks for: discoverable, verified, no attestation, the
// three kinds of key, the passkeys the person has excluded.
func TestTheQuestions(t *testing.T) {
	var made struct {
		RP        struct{ ID string } `json:"rp"`
		User      struct{ ID, Name, DisplayName string }
		Challenge string
		Params    []struct{ Alg int }   `json:"pubKeyCredParams"`
		Exclude   []struct{ ID string } `json:"excludeCredentials"`
		Selection struct {
			ResidentKey      string
			UserVerification string
		} `json:"authenticatorSelection"`
		Attestation string
	}
	text := acme.CreationOptions(ann, challenge(1), [][]byte{{1, 2, 3}}, time.Minute)
	if err := json.Unmarshal([]byte(text), &made); err != nil {
		t.Fatal(err)
	}
	if made.RP.ID != "fado.acme.example" || made.User.Name != "ann" || made.User.DisplayName != "Ann Nowak" ||
		made.User.ID != base64.RawURLEncoding.EncodeToString(ann.Handle) || len(made.Params) != 3 || len(made.Exclude) != 1 ||
		made.Selection.ResidentKey != "required" || made.Selection.UserVerification != "required" || made.Attestation != "none" {
		t.Errorf("the question to make a passkey: %s", text)
	}
	var use struct {
		RPID             string `json:"rpId"`
		Allow            []any  `json:"allowCredentials"`
		UserVerification string
	}
	text = acme.RequestOptions(challenge(2), time.Minute)
	if err := json.Unmarshal([]byte(text), &use); err != nil || use.RPID != "fado.acme.example" || use.Allow == nil || len(use.Allow) != 0 || use.UserVerification != "required" {
		t.Errorf("the question to use one: %s", text)
	}
}

// Each thing the server checks, broken alone, is refused with its own
// refusal.
func TestEachRefusal(t *testing.T) {
	type broken struct {
		name  string
		spoil func(d *webauthntest.Device)
		want  error
	}
	both := []broken{
		{"another challenge", func(d *webauthntest.Device) { d.Challenge = challenge(9) }, webauthn.ErrChallenge},
		{"a page of another site", func(d *webauthntest.Device) { d.Origin = "https://fado.acme.example.evil.example" }, webauthn.ErrOrigin},
		{"the server's host over http", func(d *webauthntest.Device) { d.Origin = "http://fado.acme.example" }, webauthn.ErrOrigin},
		{"a passkey of another host", func(d *webauthntest.Device) { d.Host = "evil.example" }, webauthn.ErrHost},
		{"nobody touched it", func(d *webauthntest.Device) { d.Present = false }, webauthn.ErrPresent},
		{"its owner not verified", func(d *webauthntest.Device) { d.Verified = false }, webauthn.ErrVerified},
	}
	for _, b := range both {
		d := webauthntest.New(webauthntest.ES256)
		b.spoil(d)
		answer, err := d.Make(acme.Origin, acme.CreationOptions(ann, challenge(1), nil, time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		c, err := webauthn.ParseCreation([]byte(answer))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := acme.CheckCreation(c, challenge(1)); !errors.Is(err, b.want) {
			t.Errorf("made, %s: %v, want %v", b.name, err, b.want)
		}

		d = webauthntest.New(webauthntest.ES256)
		m := made(t, d)
		b.spoil(d)
		if _, err := used(d, m, m.Count); !errors.Is(err, b.want) {
			t.Errorf("used, %s: %v, want %v", b.name, err, b.want)
		}
	}

	// A passkey used where one is made, and the other way.
	d := webauthntest.New(webauthntest.ES256)
	d.Type = "webauthn.get"
	answer, _ := d.Make(acme.Origin, acme.CreationOptions(ann, challenge(1), nil, time.Minute))
	c, _ := webauthn.ParseCreation([]byte(answer))
	if _, err := acme.CheckCreation(c, challenge(1)); !errors.Is(err, webauthn.ErrType) {
		t.Errorf("made with the type of a use: %v", err)
	}
	d = webauthntest.New(webauthntest.ES256)
	m := made(t, d)
	d.Type = "webauthn.create"
	if _, err := used(d, m, m.Count); !errors.Is(err, webauthn.ErrType) {
		t.Errorf("used with the type of a making: %v", err)
	}

	// A signature that does not hold, for each kind of key.
	for _, alg := range []int{webauthntest.ES256, webauthntest.EdDSA, webauthntest.RS256} {
		d := webauthntest.New(alg)
		m := made(t, d)
		d.BreakSignature = true
		if _, err := used(d, m, m.Count); !errors.Is(err, webauthn.ErrSignature) {
			t.Errorf("the kind %d, a broken signature: %v", alg, err)
		}
	}

	// Another passkey's key does not hold for this one's use.
	one, other := webauthntest.New(webauthntest.ES256), webauthntest.New(webauthntest.ES256)
	m1, m2 := made(t, one), made(t, other)
	if _, err := used(one, m2, m1.Count); !errors.Is(err, webauthn.ErrSignature) {
		t.Errorf("used with another passkey's key: %v", err)
	}

	// The counter: equal, lower, and a device that stopped counting.
	d = webauthntest.New(webauthntest.ES256)
	m = made(t, d)
	if _, err := used(d, m, 5); !errors.Is(err, webauthn.ErrCounter) {
		t.Errorf("the counter at 2 after 5: %v", err)
	}
	d.Count = 7
	if _, err := used(d, m, 7); !errors.Is(err, webauthn.ErrCounter) {
		t.Errorf("the counter at 7 after 7: %v", err)
	}
	d.Counts = false
	if _, err := used(d, m, 8); !errors.Is(err, webauthn.ErrCounter) {
		t.Errorf("the counter at 0 after 8: %v", err)
	}
}

// A key the server does not take is refused when the passkey is made:
// RSA of 1024 bits.
func TestAWeakKey(t *testing.T) {
	d := webauthntest.New(webauthntest.RS256)
	d.Bits = 1024
	answer, err := d.Make(acme.Origin, acme.CreationOptions(ann, challenge(1), nil, time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := webauthn.ParseCreation([]byte(answer))
	if _, err := acme.CheckCreation(c, challenge(1)); !errors.Is(err, webauthn.ErrKey) {
		t.Errorf("RSA of 1024 bits: %v", err)
	}
}

// What is not a passkey's answer is refused, not read on.
func TestWhatIsNotAnAnswer(t *testing.T) {
	for _, text := range []string{``, `{}`, `[]`, `{"type":"public-key"}`, `{"type":"password","id":"AAAAAAAAAAAAAAAAAAAAAA"}`,
		`{"type":"public-key","id":"AAAA","response":{"clientDataJSON":"e30"}}`,
		`{"type":"public-key","id":"AAAAAAAAAAAAAAAAAAAAAA","response":{"clientDataJSON":"e30","attestationObject":"!!"}}`} {
		if _, err := webauthn.ParseCreation([]byte(text)); !errors.Is(err, webauthn.ErrMalformed) {
			t.Errorf("made from %q: %v", text, err)
		}
		if _, err := webauthn.ParseAssertion([]byte(text)); !errors.Is(err, webauthn.ErrMalformed) {
			t.Errorf("used from %q: %v", text, err)
		}
	}
	// A maker's object that is no map, a device's data cut short, and a
	// map that names a key twice.
	d := webauthntest.New(webauthntest.ES256)
	answer, _ := d.Make(acme.Origin, acme.CreationOptions(ann, challenge(1), nil, time.Minute))
	c, _ := webauthn.ParseCreation([]byte(answer))
	for name, att := range map[string][]byte{
		"no map":        webauthntest.Encode("text"),
		"no data":       webauthntest.Encode(map[any]any{"fmt": "none", "attStmt": map[any]any{}}),
		"data cut":      webauthntest.Encode(map[any]any{"fmt": "none", "attStmt": map[any]any{}, "authData": make([]byte, 36)}),
		"a key twice":   {0xa2, 0x63, 'f', 'm', 't', 0x64, 'n', 'o', 'n', 'e', 0x63, 'f', 'm', 't', 0x64, 'n', 'o', 'n', 'e'},
		"left open":     {0xbf, 0xff},
		"a tag":         {0xc0, 0x00},
		"a float":       {0xf9, 0x00, 0x00},
		"bytes too few": {0x5a, 0xff, 0xff, 0xff, 0xff},
		"deep":          bytes.Repeat([]byte{0x81}, 40),
	} {
		c.Attestation = att
		if _, err := acme.CheckCreation(c, challenge(1)); !errors.Is(err, webauthn.ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The party of an origin: its host, without the port; an origin with a
// path is none.
func TestThePartyOfAnOrigin(t *testing.T) {
	p, err := webauthn.PartyOf("https://Fado.Acme.example:8443")
	if err != nil || p.ID != "fado.acme.example" || p.Origin != "https://fado.acme.example:8443" {
		t.Errorf("%+v %v", p, err)
	}
	for _, bad := range []string{"fado.acme.example", "https://fado.acme.example/auth", "ftp://fado.acme.example", "https://"} {
		if _, err := webauthn.PartyOf(bad); err == nil {
			t.Errorf("%s is taken", bad)
		}
	}
	if c, err := webauthn.ChallengeOf([]byte(`{"challenge":"` + base64.RawURLEncoding.EncodeToString(challenge(3)) + `"}`)); err != nil || !bytes.Equal(c, challenge(3)) {
		t.Errorf("the challenge said: %v %v", c, err)
	}
	if _, err := webauthn.ChallengeOf([]byte(`{}`)); !errors.Is(err, webauthn.ErrMalformed) || !strings.Contains(err.Error(), "passkey") {
		t.Errorf("no challenge: %v", err)
	}
}

// Whatever bytes a browser sends, the reader answers and does not fail
// in another way.
func FuzzTheDevicesData(f *testing.F) {
	d := webauthntest.New(webauthntest.ES256)
	answer, _ := d.Make(acme.Origin, acme.CreationOptions(ann, challenge(1), nil, time.Minute))
	c, _ := webauthn.ParseCreation([]byte(answer))
	f.Add(c.Attestation)
	f.Fuzz(func(t *testing.T, att []byte) {
		c := *c
		c.Attestation = att
		_, _ = acme.CheckCreation(&c, challenge(1))
	})
}
