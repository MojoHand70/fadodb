package store

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

// Document and event ids are ULIDs: 48 bits of milliseconds since the
// epoch and 80 bits of entropy, 26 characters of Crockford base32, so that
// ids sort by time as text and never collide across processes. Ids made
// within one millisecond by one generator count up, so they stay in
// order. An id is opaque text to everything else; a caller may bring its
// own document ids.

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

type idGen struct {
	mu      sync.Mutex
	lastMs  int64
	entropy [10]byte
}

// next makes an id for the moment now.
func (g *idGen) next(now time.Time) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	ms := now.UnixMilli()
	if ms <= g.lastMs {
		ms = g.lastMs
		for i := len(g.entropy) - 1; i >= 0; i-- {
			g.entropy[i]++
			if g.entropy[i] != 0 {
				break
			}
		}
	} else {
		g.lastMs = ms
		if _, err := rand.Read(g.entropy[:]); err != nil {
			panic("store: crypto/rand: " + err.Error())
		}
	}
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(ms)<<16|uint64(g.entropy[0])<<8|uint64(g.entropy[1]))
	copy(raw[8:], g.entropy[2:])
	hi, lo := binary.BigEndian.Uint64(raw[:8]), binary.BigEndian.Uint64(raw[8:])
	var out [26]byte
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = crockford[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out[:])
}
