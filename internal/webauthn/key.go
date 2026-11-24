package webauthn

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"math/big"
)

// The kinds of key a passkey may have (ADR-0088, item 5), by their
// numbers in COSE (RFC 9053): what Windows Hello, Apple, Google, Android
// and security keys make.
const (
	ES256 = -7   // ECDSA on P-256 with SHA-256
	EdDSA = -8   // Ed25519
	RS256 = -257 // RSA PKCS#1 v1.5 with SHA-256, of 2048 bits or more
)

// Algs are the kinds of key the server asks for, in the order of its
// preference.
var Algs = []int{EdDSA, ES256, RS256}

// The members of a COSE key the server reads.
const (
	coseKty = 1
	coseAlg = 3
	coseCrv = -1 // for RSA, n
	coseX   = -2 // for RSA, e
	coseY   = -3
)

// minRSA and maxRSA bound the size of an RSA key: below 2048 bits it is
// too weak, and above 8192 bits its check costs more than a sign-in is
// worth.
const (
	minRSA = 2048
	maxRSA = 8192
)

// publicKey is the public half of a passkey, ready to check a signature.
type publicKey struct {
	alg int
	ec  *ecdsa.PublicKey
	ed  ed25519.PublicKey
	rsa *rsa.PublicKey
}

// readKey reads a passkey's public key as its device wrote it, a COSE
// key, and answers it and its kind. A key of another kind, a curve
// other than the one its kind names, or a point not on the curve is
// refused.
func readKey(cose []byte) (*publicKey, error) {
	v, rest, err := readCBOR(cose)
	if err != nil || len(rest) != 0 {
		return nil, ErrKey
	}
	return keyOf(v)
}

func keyOf(v any) (*publicKey, error) {
	m, ok := v.(map[any]any)
	if !ok {
		return nil, ErrKey
	}
	kty, _ := m[int64(coseKty)].(int64)
	alg, _ := m[int64(coseAlg)].(int64)
	switch {
	case kty == 2 && alg == ES256:
		crv, _ := m[int64(coseCrv)].(int64)
		x, _ := m[int64(coseX)].([]byte)
		y, _ := m[int64(coseY)].([]byte)
		if crv != 1 || len(x) != 32 || len(y) != 32 {
			return nil, ErrKey
		}
		point := append(append([]byte{4}, x...), y...)
		pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
		if err != nil {
			return nil, ErrKey
		}
		return &publicKey{alg: ES256, ec: pub}, nil
	case kty == 1 && alg == EdDSA:
		crv, _ := m[int64(coseCrv)].(int64)
		x, _ := m[int64(coseX)].([]byte)
		if crv != 6 || len(x) != ed25519.PublicKeySize {
			return nil, ErrKey
		}
		return &publicKey{alg: EdDSA, ed: ed25519.PublicKey(x)}, nil
	case kty == 3 && alg == RS256:
		n, _ := m[int64(coseCrv)].([]byte)
		e, _ := m[int64(coseX)].([]byte)
		if len(e) == 0 || len(e) > 4 || len(n) == 0 {
			return nil, ErrKey
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n)}
		for _, c := range e {
			pub.E = pub.E<<8 | int(c)
		}
		if bits := pub.N.BitLen(); bits < minRSA || bits > maxRSA || pub.E < 3 || pub.E%2 == 0 {
			return nil, ErrKey
		}
		return &publicKey{alg: RS256, rsa: pub}, nil
	}
	return nil, ErrKey
}

// verify checks a signature over data, as the key's kind makes it: of
// its SHA-256 hash for ES256 and RS256, of the data itself for Ed25519.
func (k *publicKey) verify(data, sig []byte) bool {
	switch k.alg {
	case ES256:
		sum := sha256.Sum256(data)
		return ecdsa.VerifyASN1(k.ec, sum[:], sig)
	case EdDSA:
		return ed25519.Verify(k.ed, data, sig)
	case RS256:
		sum := sha256.Sum256(data)
		return rsa.VerifyPKCS1v15(k.rsa, crypto.SHA256, sum[:], sig) == nil
	}
	return false
}
