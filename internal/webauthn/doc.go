// Package webauthn checks passkeys, as the W3C's Web Authentication has
// them, with Go's standard library alone (ADR-0088, items 5 and 12).
//
// When a passkey is made, the server reads its public key once, from the
// device's data in CBOR. When it is used, the server checks the
// challenge it gave, the origin of its pages, the hash of its host, that
// the person touched the device and was verified by it, the signature
// with that key, and that a device which keeps a counter raised it.
// Which maker made the device is not asked. The keys are ES256 (P-256),
// EdDSA (Ed25519) and RS256 (RSA of 2048 bits or more).
//
// The package keeps nothing: the challenge, the keys and the counters
// are the caller's, which is the store. Package webauthntest is a device
// for tests.
package webauthn
