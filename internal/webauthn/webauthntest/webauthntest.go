// Package webauthntest is a device that makes and uses passkeys, for
// tests: what a phone, a computer or a security key does when a page
// asks it, with a key of each kind the server takes. It answers in the
// JSON a browser sends, and can be told to answer wrongly, one thing at
// a time, so that each refusal of the server has its test.
package webauthntest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
)

// The kinds of key, by their numbers in COSE.
const (
	ES256 = -7
	EdDSA = -8
	RS256 = -257
)

// Device is one device with one passkey once it has made it. Its
// members say how it answers; a test changes one of them to answer
// wrongly.
type Device struct {
	// Alg is the kind of key it makes.
	Alg int
	// Bits is the size of an RSA key, 2048 when left at zero.
	Bits int
	// Counts says the device keeps a counter, which it raises at each
	// use; a synced passkey keeps none and always says 0.
	Counts bool
	// Count is the counter it says next, when it keeps one.
	Count uint32
	// Present and Verified are the flags it sets: that the person
	// touched it, and that it verified them.
	Present, Verified bool
	// Synced says the passkey follows its owner to a new device.
	Synced bool
	// Origin and Host, when set, are what it says in place of the ones
	// it was asked from: a page of another site, a passkey of another
	// host.
	Origin, Host string
	// Challenge, when set, is answered in place of the one given.
	Challenge []byte
	// Type, when set, is said in place of the question asked.
	Type string
	// BreakSignature spoils the signature.
	BreakSignature bool
	// Format is the format of the maker's statement, "none" when empty.
	Format string
	// AAGUID is what it says its maker is: sixteen zeros, as Safari
	// says, when left at zero.
	AAGUID [16]byte

	// What it made: the passkey's id, the person it is for, and the key.
	ID, Handle []byte
	key        crypto.Signer
	cose       []byte
}

// New is a device that makes keys of a kind, touched and verified, with
// a counter.
func New(alg int) *Device {
	return &Device{Alg: alg, Counts: true, Count: 1, Present: true, Verified: true}
}

// options is the part of the server's question a device reads.
type options struct {
	Challenge string `json:"challenge"`
	RP        struct {
		ID string `json:"id"`
	} `json:"rp"`
	RPID string `json:"rpId"`
	User struct {
		ID string `json:"id"`
	} `json:"user"`
	Params []struct {
		Alg int `json:"alg"`
	} `json:"pubKeyCredParams"`
	Exclude []struct {
		ID string `json:"id"`
	} `json:"excludeCredentials"`
}

func unb64(s string) []byte {
	b, _ := base64.RawURLEncoding.DecodeString(s)
	return b
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// Make answers the server's question to make a passkey, asked from a
// page at origin, as a browser sends the answer back.
func (d *Device) Make(origin, question string) (string, error) {
	var o options
	if err := json.Unmarshal([]byte(question), &o); err != nil {
		return "", err
	}
	offered := false
	for _, p := range o.Params {
		offered = offered || p.Alg == d.Alg
	}
	if !offered {
		return "", fmt.Errorf("the server does not offer the key %d", d.Alg)
	}
	for _, e := range o.Exclude {
		if d.ID != nil && string(unb64(e.ID)) == string(d.ID) {
			return "", fmt.Errorf("InvalidStateError: the device holds one of the passkeys excluded")
		}
	}
	if err := d.makeKey(); err != nil {
		return "", err
	}
	d.ID = make([]byte, 32)
	_, _ = rand.Read(d.ID)
	d.Handle = unb64(o.User.ID)
	data := d.authData(o.RP.ID, true)
	format := d.Format
	if format == "" {
		format = "none"
	}
	statement := map[any]any{}
	if format == "packed" {
		statement = map[any]any{"alg": int64(d.Alg), "sig": []byte("a statement nobody reads")}
	}
	att := encode(map[any]any{"fmt": format, "attStmt": statement, "authData": data})
	client := d.clientData("webauthn.create", origin, unb64(o.Challenge))
	answer := map[string]any{"id": b64(d.ID), "rawId": b64(d.ID), "type": "public-key",
		"response": map[string]string{"clientDataJSON": b64(client), "attestationObject": b64(att)}}
	text, err := json.Marshal(answer)
	return string(text), err
}

// Use answers the server's question to use a passkey, asked from a page
// at origin, with the passkey the device made.
func (d *Device) Use(origin, question string) (string, error) {
	var o options
	if err := json.Unmarshal([]byte(question), &o); err != nil {
		return "", err
	}
	if d.key == nil {
		return "", fmt.Errorf("the device holds no passkey")
	}
	data := d.authData(o.RPID, false)
	client := d.clientData("webauthn.get", origin, unb64(o.Challenge))
	sum := sha256.Sum256(client)
	sig, err := d.sign(append(append([]byte{}, data...), sum[:]...))
	if err != nil {
		return "", err
	}
	if d.BreakSignature {
		sig[len(sig)/2] ^= 0xff
	}
	answer := map[string]any{"id": b64(d.ID), "rawId": b64(d.ID), "type": "public-key",
		"response": map[string]string{"clientDataJSON": b64(client), "authenticatorData": b64(data), "signature": b64(sig), "userHandle": b64(d.Handle)}}
	text, err := json.Marshal(answer)
	return string(text), err
}

func (d *Device) clientData(kind, origin string, challenge []byte) []byte {
	if d.Type != "" {
		kind = d.Type
	}
	if d.Origin != "" {
		origin = d.Origin
	}
	if d.Challenge != nil {
		challenge = d.Challenge
	}
	text, _ := json.Marshal(map[string]any{"type": kind, "challenge": b64(challenge), "origin": origin, "crossOrigin": false})
	return text
}

// authData is the device's data: the hash of the host, the flags, the
// counter, and for a new passkey its id and its key.
func (d *Device) authData(host string, made bool) []byte {
	if d.Host != "" {
		host = d.Host
	}
	sum := sha256.Sum256([]byte(host))
	out := append([]byte{}, sum[:]...)
	var flags byte
	if d.Present {
		flags |= 0x01
	}
	if d.Verified {
		flags |= 0x04
	}
	if d.Synced {
		flags |= 0x08 | 0x10
	}
	if made {
		flags |= 0x40
	}
	out = append(out, flags)
	var count uint32
	if d.Counts {
		count = d.Count
		d.Count++
	}
	out = binary.BigEndian.AppendUint32(out, count)
	if made {
		out = append(out, d.AAGUID[:]...)
		out = binary.BigEndian.AppendUint16(out, uint16(len(d.ID)))
		out = append(out, d.ID...)
		out = append(out, d.cose...)
	}
	return out
}

func (d *Device) makeKey() error {
	switch d.Alg {
	case ES256:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		point, err := k.PublicKey.Bytes()
		if err != nil {
			return err
		}
		d.key = k
		d.cose = encode(map[any]any{int64(1): int64(2), int64(3): int64(ES256), int64(-1): int64(1), int64(-2): point[1:33], int64(-3): point[33:]})
	case EdDSA:
		pub, k, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		d.key = k
		d.cose = encode(map[any]any{int64(1): int64(1), int64(3): int64(EdDSA), int64(-1): int64(6), int64(-2): []byte(pub)})
	case RS256:
		bits := d.Bits
		if bits == 0 {
			bits = 2048
		}
		k, err := rsa.GenerateKey(rand.Reader, bits)
		if err != nil {
			return err
		}
		d.key = k
		e := big.NewInt(int64(k.E)).Bytes()
		d.cose = encode(map[any]any{int64(1): int64(3), int64(3): int64(RS256), int64(-1): k.N.Bytes(), int64(-2): e})
	default:
		return fmt.Errorf("no key of the kind %d", d.Alg)
	}
	return nil
}

func (d *Device) sign(data []byte) ([]byte, error) {
	switch k := d.key.(type) {
	case ed25519.PrivateKey:
		return ed25519.Sign(k, data), nil
	case *ecdsa.PrivateKey:
		sum := sha256.Sum256(data)
		return ecdsa.SignASN1(rand.Reader, k, sum[:])
	case *rsa.PrivateKey:
		sum := sha256.Sum256(data)
		return rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:])
	}
	return nil, fmt.Errorf("no key")
}

// encode writes CBOR as a device does: whole numbers, byte strings,
// texts and maps, the keys of a map in CTAP2's canonical order.
func encode(v any) []byte {
	var out []byte
	head := func(major byte, n uint64) {
		switch {
		case n < 24:
			out = append(out, major<<5|byte(n))
		case n <= 0xff:
			out = append(out, major<<5|24, byte(n))
		case n <= 0xffff:
			out = append(out, major<<5|25)
			out = binary.BigEndian.AppendUint16(out, uint16(n))
		case n <= 0xffffffff:
			out = append(out, major<<5|26)
			out = binary.BigEndian.AppendUint32(out, uint32(n))
		default:
			out = append(out, major<<5|27)
			out = binary.BigEndian.AppendUint64(out, n)
		}
	}
	switch x := v.(type) {
	case int64:
		if x >= 0 {
			head(0, uint64(x))
		} else {
			head(1, uint64(-1-x))
		}
	case []byte:
		head(2, uint64(len(x)))
		out = append(out, x...)
	case string:
		head(3, uint64(len(x)))
		out = append(out, x...)
	case map[any]any:
		head(5, uint64(len(x)))
		keys := make([][]byte, 0, len(x))
		items := map[string][]byte{}
		for k, item := range x {
			ek := encode(k)
			keys = append(keys, ek)
			items[string(ek)] = encode(item)
		}
		sort.Slice(keys, func(i, j int) bool {
			if len(keys[i]) != len(keys[j]) {
				return len(keys[i]) < len(keys[j])
			}
			return string(keys[i]) < string(keys[j])
		})
		for _, k := range keys {
			out = append(out, k...)
			out = append(out, items[string(k)]...)
		}
	default:
		panic(fmt.Sprintf("webauthntest: no CBOR for %T", v))
	}
	return out
}

// Encode is the CBOR a device writes of a value, for a test that sends
// the server something no device would.
func Encode(v any) []byte { return encode(v) }
