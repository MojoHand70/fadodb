package webauthn

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
)

// Party is the server as a passkey knows it: the host its passkeys are
// made for, which WebAuthn calls the relying party's id, and the one
// origin its pages are at. A passkey works on that host alone.
type Party struct {
	ID     string
	Origin string
}

// PartyOf is the party of the origin the server's pages are at, such as
// https://fado.acme.example: its host, without the port.
func PartyOf(origin string) (Party, error) {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return Party{}, errors.New("the origin of the server's pages is written as https://host or https://host:port")
	}
	return Party{ID: strings.ToLower(u.Hostname()), Origin: u.Scheme + "://" + strings.ToLower(u.Host)}, nil
}

// The refusals, one for each thing that was checked, so that a test
// shows which held. A page says none of them to the person: a sign-in is
// refused in the same words whatever was wrong.
var (
	ErrMalformed = errors.New("what the browser sent is not a passkey's answer")
	ErrType      = errors.New("the answer is to another question: making a passkey where one was used, or the other way")
	ErrChallenge = errors.New("the answer is not to the challenge the server gave")
	ErrOrigin    = errors.New("the answer comes from another page than the server's")
	ErrHost      = errors.New("the passkey is for another host")
	ErrPresent   = errors.New("nobody touched the device")
	ErrVerified  = errors.New("the device did not verify its owner with a fingerprint, a face or its PIN")
	ErrKey       = errors.New("the passkey's key is not of a kind the server takes")
	ErrSignature = errors.New("the signature does not hold")
	ErrCounter   = errors.New("the device's counter did not rise: the passkey may have been copied")
)

// The flags of a device's data (WebAuthn, 6.1).
const (
	flagPresent  = 0x01
	flagVerified = 0x04
	flagEligible = 0x08
	flagSynced   = 0x10
	flagAttested = 0x40
	flagExtended = 0x80
)

// Made is a new passkey, as the server keeps it: the id the device knows
// it by, its public key as the device wrote it, the kind of the key, the
// device's counter, whether it is a synced passkey, which follows its
// owner to a new phone, and the name of its maker, by its AAGUID, when
// the table of makers holds it.
type Made struct {
	Credential []byte
	Key        []byte
	Alg        int
	Count      uint32
	Synced     bool
	Maker      string
}

// Used is what a passkey's use says of it: its counter now, and whether
// it is synced.
type Used struct {
	Count  uint32
	Synced bool
}

// Creation is what a browser answers when it makes a passkey, as
// PublicKeyCredential.toJSON writes it.
type Creation struct {
	Credential  []byte
	ClientData  []byte
	Attestation []byte
}

// Assertion is what a browser answers when a passkey is used.
type Assertion struct {
	Credential []byte
	ClientData []byte
	AuthData   []byte
	Signature  []byte
	UserHandle []byte
}

// sent is the JSON of a browser's answer.
type sent struct {
	ID       string `json:"id"`
	RawID    string `json:"rawId"`
	Type     string `json:"type"`
	Response struct {
		ClientDataJSON    string `json:"clientDataJSON"`
		AttestationObject string `json:"attestationObject"`
		AuthenticatorData string `json:"authenticatorData"`
		Signature         string `json:"signature"`
		UserHandle        string `json:"userHandle"`
	} `json:"response"`
}

// unb64 reads base64url, with or without its padding.
func unb64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// b64 writes base64url without padding, as WebAuthn's JSON has it.
func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func readSent(text []byte) (*sent, []byte, []byte, error) {
	var s sent
	if json.Unmarshal(text, &s) != nil || s.Type != "public-key" {
		return nil, nil, nil, ErrMalformed
	}
	id := s.RawID
	if id == "" {
		id = s.ID
	}
	credential, err1 := unb64(id)
	client, err2 := unb64(s.Response.ClientDataJSON)
	if err1 != nil || err2 != nil || len(credential) < 16 || len(credential) > 1023 || len(client) == 0 {
		return nil, nil, nil, ErrMalformed
	}
	return &s, credential, client, nil
}

// ParseCreation reads what a browser answers when it makes a passkey.
func ParseCreation(text []byte) (*Creation, error) {
	s, credential, client, err := readSent(text)
	if err != nil {
		return nil, err
	}
	att, err := unb64(s.Response.AttestationObject)
	if err != nil || len(att) == 0 {
		return nil, ErrMalformed
	}
	return &Creation{Credential: credential, ClientData: client, Attestation: att}, nil
}

// ParseAssertion reads what a browser answers when a passkey is used.
func ParseAssertion(text []byte) (*Assertion, error) {
	s, credential, client, err := readSent(text)
	if err != nil {
		return nil, err
	}
	data, err1 := unb64(s.Response.AuthenticatorData)
	sig, err2 := unb64(s.Response.Signature)
	handle, err3 := unb64(s.Response.UserHandle)
	if err1 != nil || err2 != nil || err3 != nil || len(data) == 0 || len(sig) == 0 {
		return nil, ErrMalformed
	}
	return &Assertion{Credential: credential, ClientData: client, AuthData: data, Signature: sig, UserHandle: handle}, nil
}

// clientData is what the browser says of the question it answered.
type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
	TopOrigin   string `json:"topOrigin"`
}

// ChallengeOf is the challenge a browser says it answers, so that the
// server finds what it gave. It is checked again with the rest.
func ChallengeOf(clientDataJSON []byte) ([]byte, error) {
	var c clientData
	if json.Unmarshal(clientDataJSON, &c) != nil {
		return nil, ErrMalformed
	}
	got, err := unb64(c.Challenge)
	if err != nil || len(got) == 0 {
		return nil, ErrMalformed
	}
	return got, nil
}

// checkClient checks what the browser says it answered: the question,
// the challenge the server gave, and the origin of the server's pages,
// in no frame of another site.
func (p Party) checkClient(raw []byte, kind string, challenge []byte) error {
	var c clientData
	if json.Unmarshal(raw, &c) != nil {
		return ErrMalformed
	}
	if c.Type != kind {
		return ErrType
	}
	got, err := unb64(c.Challenge)
	if err != nil || len(challenge) == 0 || subtle.ConstantTimeCompare(got, challenge) != 1 {
		return ErrChallenge
	}
	if c.Origin != p.Origin || c.CrossOrigin || (c.TopOrigin != "" && c.TopOrigin != p.Origin) {
		return ErrOrigin
	}
	return nil
}

// authData is a device's data (WebAuthn, 6.1): the hash of the host, the
// flags, the counter, and for a new passkey its id and its key.
type authData struct {
	host       []byte
	flags      byte
	count      uint32
	aaguid     [16]byte
	credential []byte
	key        []byte
}

func readAuthData(b []byte) (*authData, error) {
	if len(b) < 37 {
		return nil, ErrMalformed
	}
	d := &authData{host: b[:32], flags: b[32], count: binary.BigEndian.Uint32(b[33:37])}
	if d.flags&flagSynced != 0 && d.flags&flagEligible == 0 {
		return nil, ErrMalformed
	}
	rest := b[37:]
	if d.flags&flagAttested != 0 {
		if len(rest) < 18 {
			return nil, ErrMalformed
		}
		copy(d.aaguid[:], rest[:16])
		n := int(binary.BigEndian.Uint16(rest[16:18]))
		rest = rest[18:]
		if n < 16 || n > 1023 || len(rest) < n {
			return nil, ErrMalformed
		}
		d.credential, rest = rest[:n], rest[n:]
		_, after, err := readCBOR(rest)
		if err != nil {
			return nil, ErrMalformed
		}
		d.key, rest = rest[:len(rest)-len(after)], after
	}
	if d.flags&flagExtended != 0 {
		v, after, err := readCBOR(rest)
		if _, isMap := v.(map[any]any); err != nil || !isMap {
			return nil, ErrMalformed
		}
		rest = after
	}
	if len(rest) != 0 {
		return nil, ErrMalformed
	}
	return d, nil
}

// checkDevice checks a device's data: made for this host, the device
// touched and its owner verified.
func (p Party) checkDevice(d *authData) error {
	host := sha256.Sum256([]byte(p.ID))
	if !bytes.Equal(d.host, host[:]) {
		return ErrHost
	}
	if d.flags&flagPresent == 0 {
		return ErrPresent
	}
	if d.flags&flagVerified == 0 {
		return ErrVerified
	}
	return nil
}

// CheckCreation checks a new passkey against the challenge the server
// gave, and answers what the server keeps of it. Which maker made the
// device is not asked (ADR-0088, item 5): the statement of a device's
// maker, whatever its format, is not read.
func (p Party) CheckCreation(c *Creation, challenge []byte) (*Made, error) {
	if err := p.checkClient(c.ClientData, "webauthn.create", challenge); err != nil {
		return nil, err
	}
	v, rest, err := readCBOR(c.Attestation)
	m, _ := v.(map[any]any)
	if err != nil || len(rest) != 0 || m == nil {
		return nil, ErrMalformed
	}
	_, named := m["fmt"].(string)
	_, statement := m["attStmt"].(map[any]any)
	raw, _ := m["authData"].([]byte)
	if !named || !statement || raw == nil {
		return nil, ErrMalformed
	}
	d, err := readAuthData(raw)
	if err != nil {
		return nil, err
	}
	if err := p.checkDevice(d); err != nil {
		return nil, err
	}
	if d.flags&flagAttested == 0 || !bytes.Equal(d.credential, c.Credential) {
		return nil, ErrMalformed
	}
	key, err := readKey(d.key)
	if err != nil {
		return nil, err
	}
	return &Made{Credential: d.credential, Key: d.key, Alg: key.alg, Count: d.count, Synced: d.flags&flagSynced != 0, Maker: MakerOf(d.aaguid)}, nil
}

// CheckAssertion checks the use of a passkey the server keeps, with its
// key and the counter it last had, against the challenge the server
// gave. A device that keeps a counter must have raised it: one that did
// not shows a copy of the passkey, and is refused.
func (p Party) CheckAssertion(a *Assertion, challenge, key []byte, count uint32) (*Used, error) {
	if err := p.checkClient(a.ClientData, "webauthn.get", challenge); err != nil {
		return nil, err
	}
	d, err := readAuthData(a.AuthData)
	if err != nil {
		return nil, err
	}
	if err := p.checkDevice(d); err != nil {
		return nil, err
	}
	pub, err := readKey(key)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(a.ClientData)
	signed := append(append(make([]byte, 0, len(a.AuthData)+len(sum)), a.AuthData...), sum[:]...)
	if !pub.verify(signed, a.Signature) {
		return nil, ErrSignature
	}
	if (count != 0 || d.count != 0) && d.count <= count {
		return nil, ErrCounter
	}
	return &Used{Count: d.count, Synced: d.flags&flagSynced != 0}, nil
}

// User is the person a passkey is made for, as the device shows them:
// the 16 random bytes it knows them by (never their id), the name they
// sign in as, and the name it shows.
type User struct {
	Handle  []byte
	Name    string
	Display string
}

// A question to the browser, as WebAuthn's JSON writes it
// (PublicKeyCredentialCreationOptionsJSON and its kin).
type (
	creationOptions struct {
		RP        rpEntity       `json:"rp"`
		User      userEntity     `json:"user"`
		Challenge string         `json:"challenge"`
		Params    []param        `json:"pubKeyCredParams"`
		Timeout   int64          `json:"timeout"`
		Exclude   []descriptor   `json:"excludeCredentials"`
		Selection authenticators `json:"authenticatorSelection"`
		Attest    string         `json:"attestation"`
	}
	requestOptions struct {
		Challenge string       `json:"challenge"`
		RPID      string       `json:"rpId"`
		Timeout   int64        `json:"timeout"`
		Allow     []descriptor `json:"allowCredentials"`
		Verify    string       `json:"userVerification"`
	}
	rpEntity struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	userEntity struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Display string `json:"displayName"`
	}
	param struct {
		Type string `json:"type"`
		Alg  int    `json:"alg"`
	}
	descriptor struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	authenticators struct {
		ResidentKey string `json:"residentKey"`
		Require     bool   `json:"requireResidentKey"`
		Verify      string `json:"userVerification"`
	}
)

// CreationOptions is the question that asks the browser to make a
// passkey (ADR-0088, item 5): for this host, kept by the device with who
// it is for, opened by the person's fingerprint, face or PIN, of one of
// the three kinds of key, and none of the passkeys the person already
// has on that device.
func (p Party) CreationOptions(u User, challenge []byte, exclude [][]byte, timeout time.Duration) string {
	o := creationOptions{
		RP:        rpEntity{ID: p.ID, Name: p.ID},
		User:      userEntity{ID: b64(u.Handle), Name: u.Name, Display: u.Display},
		Challenge: b64(challenge),
		Timeout:   timeout.Milliseconds(),
		Exclude:   []descriptor{},
		Selection: authenticators{ResidentKey: "required", Require: true, Verify: "required"},
		Attest:    "none",
	}
	for _, alg := range Algs {
		o.Params = append(o.Params, param{Type: "public-key", Alg: alg})
	}
	for _, c := range exclude {
		o.Exclude = append(o.Exclude, descriptor{Type: "public-key", ID: b64(c)})
	}
	text, _ := json.Marshal(o)
	return string(text)
}

// RequestOptions is the question that asks the browser to use one of
// the passkeys it holds for this host, whoever it is for: the passkey
// says who the person is.
func (p Party) RequestOptions(challenge []byte, timeout time.Duration) string {
	text, _ := json.Marshal(requestOptions{Challenge: b64(challenge), RPID: p.ID, Timeout: timeout.Milliseconds(), Allow: []descriptor{}, Verify: "required"})
	return string(text)
}
