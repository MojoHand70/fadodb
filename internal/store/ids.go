package store

import (
	"crypto/rand"
	"encoding/binary"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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
	out := g.make(now)
	return string(out[:])
}

// change makes an id for the moment now with who made the change after
// it, as one text (ADR-0099, step 3): the id, the length of who in
// characters in six digits, who, and for whom. changeSQL takes it apart.
// A text that is not UTF-8 is made so, since SQLite counts characters.
func (g *idGen) change(now time.Time, who, whoFor string) string {
	if !utf8.ValidString(who) {
		who = strings.ToValidUTF8(who, "\uFFFD")
	}
	if !utf8.ValidString(whoFor) {
		whoFor = strings.ToValidUTF8(whoFor, "\uFFFD")
	}
	n := utf8.RuneCountInString(who)
	if n > 999_999 {
		// Six digits: an id longer than that is kept cut.
		who, n = string([]rune(who)[:999_999]), 999_999
	}
	out := g.make(now)
	var buf [96]byte
	b := append(buf[:0], out[:]...)
	b = strconv.AppendInt(append(b, "000000"[:6-digits(n)]...), int64(n), 10)
	b = append(append(b, who...), whoFor...)
	return string(b)
}

// digits is how many decimal digits n has, 1 for 0.
func digits(n int) int {
	d := 1
	for n >= 10 {
		n /= 10
		d++
	}
	return d
}

// make makes the characters of an id for the moment now.
func (g *idGen) make(now time.Time) [26]byte {
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
	return out
}
