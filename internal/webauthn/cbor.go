package webauthn

import (
	"encoding/binary"
	"errors"
)

// The part of CBOR (RFC 8949) that a passkey's device writes: whole
// numbers, byte strings, texts, arrays and maps of a known length, and
// the simple values true, false and null. A device writes CTAP2's
// canonical form, which has no tags, no floats and no length left open,
// so the reader refuses them, and a map that names a key twice.

// maxDepth is how deep arrays and maps nest: a device's key and its
// statement are three deep at most.
const maxDepth = 8

var errCBOR = errors.New("the device's data is not CBOR as a passkey writes it")

// readCBOR reads one item at the start of b, and answers it and what
// follows it. An item is an int64, a []byte, a string, a []any, a
// map[any]any whose keys are int64 or string, a bool, or nil.
func readCBOR(b []byte) (any, []byte, error) { return readItem(b, 0) }

func readItem(b []byte, depth int) (any, []byte, error) {
	if len(b) == 0 || depth > maxDepth {
		return nil, nil, errCBOR
	}
	major, info := b[0]>>5, b[0]&0x1f
	n, b, err := readHead(info, b[1:])
	if err != nil {
		return nil, nil, err
	}
	switch major {
	case 0:
		if n > 1<<63-1 {
			return nil, nil, errCBOR
		}
		return int64(n), b, nil
	case 1:
		if n > 1<<63-1 {
			return nil, nil, errCBOR
		}
		return -1 - int64(n), b, nil
	case 2, 3:
		if n > uint64(len(b)) {
			return nil, nil, errCBOR
		}
		if major == 2 {
			return append([]byte(nil), b[:n]...), b[n:], nil
		}
		return string(b[:n]), b[n:], nil
	case 4:
		// Each item takes a byte at least, so a length beyond what is
		// left cannot be true, and is not allocated.
		if n > uint64(len(b)) {
			return nil, nil, errCBOR
		}
		list := make([]any, 0, n)
		for i := uint64(0); i < n; i++ {
			var v any
			if v, b, err = readItem(b, depth+1); err != nil {
				return nil, nil, err
			}
			list = append(list, v)
		}
		return list, b, nil
	case 5:
		if n > uint64(len(b))/2 {
			return nil, nil, errCBOR
		}
		m := make(map[any]any, n)
		for i := uint64(0); i < n; i++ {
			var k, v any
			if k, b, err = readItem(b, depth+1); err != nil {
				return nil, nil, err
			}
			switch k.(type) {
			case int64, string:
			default:
				return nil, nil, errCBOR
			}
			if _, twice := m[k]; twice {
				return nil, nil, errCBOR
			}
			if v, b, err = readItem(b, depth+1); err != nil {
				return nil, nil, err
			}
			m[k] = v
		}
		return m, b, nil
	case 7:
		switch info {
		case 20:
			return false, b, nil
		case 21:
			return true, b, nil
		case 22:
			return nil, b, nil
		}
	}
	return nil, nil, errCBOR
}

// readHead reads the number that follows the first byte of an item: in
// that byte, or in the 1, 2, 4 or 8 bytes after it.
func readHead(info byte, b []byte) (uint64, []byte, error) {
	switch {
	case info < 24:
		return uint64(info), b, nil
	case info == 24 && len(b) >= 1:
		return uint64(b[0]), b[1:], nil
	case info == 25 && len(b) >= 2:
		return uint64(binary.BigEndian.Uint16(b)), b[2:], nil
	case info == 26 && len(b) >= 4:
		return uint64(binary.BigEndian.Uint32(b)), b[4:], nil
	case info == 27 && len(b) >= 8:
		return binary.BigEndian.Uint64(b), b[8:], nil
	}
	// 28 to 30 are no length, and 31 is a length left open.
	return 0, nil, errCBOR
}
