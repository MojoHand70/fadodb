// Package qrcode draws a QR code of a short text, such as a link, for a
// page of the server (ADR-0088, item 3): the code a phone's camera reads
// to add a passkey on the phone. It is code of our own over Go's
// standard library (ADR-0088, item 12), and it does one thing: the
// bytes of the text, at the level of error correction M, which restores
// about 15% of the code, in the smallest of the versions 1 to 20 that
// holds them. A link of the server fits in version 6 or 7.
//
// The steps follow ISO/IEC 18004: the bits of the text, its codewords
// cut into blocks, each block's Reed-Solomon codewords over GF(256),
// the blocks interleaved, the codewords laid in the matrix around its
// patterns, and the one of the eight masks that scores the least
// penalty, whose number the format information carries.
package qrcode

import (
	"errors"
	"strconv"
	"strings"
)

// MaxVersion is the largest version Encode makes: 97 modules a side,
// 666 bytes of text.
const MaxVersion = 20

// Code is a QR code: Size modules a side, each dark or light, without
// the quiet zone around it.
type Code struct {
	Size int
	dark []bool
}

// Dark reports whether the module at column x and row y is dark.
func (c *Code) Dark(x, y int) bool { return c.dark[y*c.Size+x] }

// Encode makes the QR code of text, in the smallest version that holds
// its bytes. A text longer than a version 20 holds is refused.
func Encode(text string) (*Code, error) {
	v := versionFor(len(text))
	if v == 0 {
		return nil, errors.New("qrcode: " + strconv.Itoa(len(text)) + " bytes are more than a code of version " + strconv.Itoa(MaxVersion) + " holds")
	}
	return build(v, []byte(text), -1), nil
}

// versionFor answers the smallest version that holds n bytes, or 0.
func versionFor(n int) int {
	for v := 1; v <= MaxVersion; v++ {
		if 4+countBits(v)+8*n <= 8*dataCodewords(v) {
			return v
		}
	}
	return 0
}

// SVG draws the code as an SVG element for a page, dark modules on
// white with the quiet zone of four modules the standard asks for, so
// that a camera reads it on a dark page too. Its size is the page's to
// set. label names the picture for a reader of the screen; it is
// written as given, so the caller escapes it.
func (c *Code) SVG(label string) string {
	const quiet = 4
	n := strconv.Itoa(c.Size + 2*quiet)
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ` + n + " " + n + `" shape-rendering="crispEdges" role="img" aria-label="` + label + `">`)
	b.WriteString(`<rect width="` + n + `" height="` + n + `" fill="#fff"/><path fill="#000" d="`)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; {
			if !c.Dark(x, y) {
				x++
				continue
			}
			from := x
			for x < c.Size && c.Dark(x, y) {
				x++
			}
			w := strconv.Itoa(x - from)
			b.WriteString("M" + strconv.Itoa(from+quiet) + " " + strconv.Itoa(y+quiet) + "h" + w + "v1h-" + w + "z")
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}

// blocks is how the codewords of a version are cut at level M: the
// Reed-Solomon codewords of each block, and the blocks of the first
// group with their data codewords; the blocks of the second group hold
// one data codeword more.
type blocks struct{ ec, n1, d1, n2 int }

// blocksOf answers the blocks of a version, 1 to MaxVersion, at level M
// (ISO/IEC 18004, table 9). The test holds each against the modules the
// version has free for data.
func blocksOf(v int) blocks {
	return [MaxVersion + 1]blocks{
		{},
		{10, 1, 16, 0}, {16, 1, 28, 0}, {26, 1, 44, 0}, {18, 2, 32, 0}, {24, 2, 43, 0},
		{16, 4, 27, 0}, {18, 4, 31, 0}, {22, 2, 38, 2}, {22, 3, 36, 2}, {26, 4, 43, 1},
		{30, 1, 50, 4}, {22, 6, 36, 2}, {22, 8, 37, 1}, {24, 4, 40, 5}, {24, 5, 41, 5},
		{28, 7, 45, 3}, {28, 10, 46, 1}, {26, 9, 43, 4}, {26, 3, 44, 11}, {26, 3, 41, 13},
	}[v]
}

// dataCodewords answers how many codewords of a version carry data.
func dataCodewords(v int) int {
	b := blocksOf(v)
	return b.n1*b.d1 + b.n2*(b.d1+1)
}

// countBits is the length of the count of bytes: 8 bits up to version
// 9, 16 after.
func countBits(v int) int {
	if v <= 9 {
		return 8
	}
	return 16
}

// build makes the code of a version, with the mask given, or with the
// one of least penalty when mask is -1.
func build(v int, text []byte, mask int) *Code {
	m := newMatrix(v)
	m.place(interleave(v, codewords(v, text)))
	if mask < 0 {
		best := 0
		for k := 0; k < 8; k++ {
			m.format(k)
			m.mask(k)
			p := m.penalty()
			m.mask(k)
			if k == 0 || p < best {
				best, mask = p, k
			}
		}
	}
	m.format(mask)
	m.mask(mask)
	return &Code{Size: m.size, dark: m.dark}
}

// bits writes bits, the first of each byte its highest.
type bits struct {
	b []byte
	n int
}

func (w *bits) put(v, k int) {
	for i := k - 1; i >= 0; i-- {
		if w.n%8 == 0 {
			w.b = append(w.b, 0)
		}
		if v>>i&1 != 0 {
			w.b[w.n/8] |= 0x80 >> (w.n % 8)
		}
		w.n++
	}
}

// codewords is the data of a version: the mode of bytes, their count,
// the bytes, up to four bits of 0 that end them, then the codewords
// 0xEC and 0x11 by turns to fill the version.
func codewords(v int, text []byte) []byte {
	room := 8 * dataCodewords(v)
	var w bits
	w.put(0b0100, 4)
	w.put(len(text), countBits(v))
	for _, c := range text {
		w.put(int(c), 8)
	}
	w.put(0, min(4, room-w.n))
	if r := w.n % 8; r != 0 {
		w.put(0, 8-r)
	}
	for pad := 0xEC; w.n < room; pad ^= 0xEC ^ 0x11 {
		w.put(pad, 8)
	}
	return w.b
}

// interleave cuts the data into the version's blocks, adds each block's
// Reed-Solomon codewords, and takes the first codeword of each block,
// then the second, and so on, the data first and the rest after.
func interleave(v int, data []byte) []byte {
	b := blocksOf(v)
	divisor := generator(b.ec)
	var parts, ecs [][]byte
	at := 0
	for i := 0; i < b.n1+b.n2; i++ {
		n := b.d1
		if i >= b.n1 {
			n++
		}
		parts = append(parts, data[at:at+n])
		ecs = append(ecs, remainder(data[at:at+n], divisor))
		at += n
	}
	out := make([]byte, 0, len(data)+b.ec*len(parts))
	for i := 0; i <= b.d1; i++ {
		for _, p := range parts {
			if i < len(p) {
				out = append(out, p[i])
			}
		}
	}
	for i := 0; i < b.ec; i++ {
		for _, e := range ecs {
			out = append(out, e[i])
		}
	}
	return out
}

// mul multiplies in GF(256) modulo x^8 + x^4 + x^3 + x^2 + 1.
func mul(a, b byte) byte {
	var r byte
	for ; b != 0; b >>= 1 {
		if b&1 != 0 {
			r ^= a
		}
		if a&0x80 != 0 {
			a = a<<1 ^ 0x1D
		} else {
			a <<= 1
		}
	}
	return r
}

// generator is the product of (x - 2^i) for i below degree, its
// coefficients highest first, without the leading 1.
func generator(degree int) []byte {
	g := make([]byte, degree)
	g[degree-1] = 1
	root := byte(1)
	for i := 0; i < degree; i++ {
		for j := range g {
			g[j] = mul(g[j], root)
			if j+1 < len(g) {
				g[j] ^= g[j+1]
			}
		}
		root = mul(root, 2)
	}
	return g
}

// remainder is the remainder of data times x^degree divided by the
// generator: the block's Reed-Solomon codewords.
func remainder(data, divisor []byte) []byte {
	r := make([]byte, len(divisor))
	for _, c := range data {
		f := c ^ r[0]
		copy(r, r[1:])
		r[len(r)-1] = 0
		for i, d := range divisor {
			r[i] ^= mul(d, f)
		}
	}
	return r
}

// matrix is a code being made: its modules, and which of them belong to
// its patterns and so hold no data and take no mask.
type matrix struct {
	size  int
	dark  []bool
	fixed []bool
}

// newMatrix draws the patterns of a version: the timing lines, the
// three finders with their white borders, the alignment patterns, the
// room of the format information and, from version 7, the version.
func newMatrix(v int) *matrix {
	s := 4*v + 17
	m := &matrix{size: s, dark: make([]bool, s*s), fixed: make([]bool, s*s)}
	for i := 0; i < s; i++ {
		m.set(6, i, i%2 == 0)
		m.set(i, 6, i%2 == 0)
	}
	for _, c := range [][2]int{{3, 3}, {s - 4, 3}, {3, s - 4}} {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				x, y := c[0]+dx, c[1]+dy
				if x >= 0 && x < s && y >= 0 && y < s {
					d := max(abs(dx), abs(dy))
					m.set(x, y, d != 2 && d != 4)
				}
			}
		}
	}
	at := alignments(v)
	last := len(at) - 1
	for i, y := range at {
		for j, x := range at {
			if (i == 0 && j == 0) || (i == 0 && j == last) || (i == last && j == 0) {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					m.set(x+dx, y+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
	m.format(0)
	if v >= 7 {
		rem := v
		for i := 0; i < 12; i++ {
			rem = rem<<1 ^ (rem>>11)*0x1F25
		}
		word := v<<12 | rem
		for i := 0; i < 18; i++ {
			d := word>>i&1 != 0
			a, b := s-11+i%3, i/3
			m.set(a, b, d)
			m.set(b, a, d)
		}
	}
	return m
}

// alignments answers the rows and columns of a version's alignment
// patterns: none in version 1, then 6, the last 7 modules from the far
// side, and others evenly between.
func alignments(v int) []int {
	if v == 1 {
		return nil
	}
	n := v/7 + 2
	step := (v*8 + n*3 + 5) / (n*4 - 4) * 2
	at := make([]int, n)
	at[0] = 6
	for i, p := n-1, 4*v+10; i >= 1; i, p = i-1, p-step {
		at[i] = p
	}
	return at
}

func (m *matrix) set(x, y int, dark bool) {
	m.dark[y*m.size+x] = dark
	m.fixed[y*m.size+x] = true
}

// format draws the format information of level M and a mask, twice:
// five bits, ten of a BCH code, masked with 101010000010010; and the
// one module that is always dark.
func (m *matrix) format(mask int) {
	data := mask // level M is 00
	rem := data
	for i := 0; i < 10; i++ {
		rem = rem<<1 ^ (rem>>9)*0x537
	}
	word := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return word>>i&1 != 0 }
	s := m.size
	for i := 0; i <= 5; i++ {
		m.set(8, i, bit(i))
	}
	m.set(8, 7, bit(6))
	m.set(8, 8, bit(7))
	m.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		m.set(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		m.set(s-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		m.set(8, s-15+i, bit(i))
	}
	m.set(8, s-8, true)
}

// place lays the codewords, highest bit first, in columns of two from
// the bottom right, up and down by turns, past the patterns and the
// timing column. The modules left over stay light.
func (m *matrix) place(data []byte) {
	s, i := m.size, 0
	for right := s - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < s; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				y := vert
				if (right+1)&2 == 0 {
					y = s - 1 - vert
				}
				if !m.fixed[y*s+x] && i < len(data)*8 {
					m.dark[y*s+x] = data[i>>3]>>(7-i&7)&1 != 0
					i++
				}
			}
		}
	}
}

// masked reports whether mask k turns the module at column x and row y.
func masked(k, x, y int) bool {
	switch k {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (x/3+y/2)%2 == 0
	case 5:
		return x*y%2+x*y%3 == 0
	case 6:
		return (x*y%2+x*y%3)%2 == 0
	default:
		return ((x+y)%2+x*y%3)%2 == 0
	}
}

// mask turns the data modules that mask k names; done twice, it undoes
// itself.
func (m *matrix) mask(k int) {
	for y := 0; y < m.size; y++ {
		for x := 0; x < m.size; x++ {
			if !m.fixed[y*m.size+x] && masked(k, x, y) {
				m.dark[y*m.size+x] = !m.dark[y*m.size+x]
			}
		}
	}
}

// penalty scores how hard a code is to read, by the four rules of the
// standard: runs of five or more alike in a line, blocks of two by two
// alike, the look of a finder in a line, and dark far from half.
func (m *matrix) penalty() int {
	s, p := m.size, 0
	at := func(x, y int) bool { return m.dark[y*s+x] }
	for i := 0; i < s; i++ {
		p += line(s, func(j int) bool { return at(j, i) })
		p += line(s, func(j int) bool { return at(i, j) })
	}
	for y := 0; y < s-1; y++ {
		for x := 0; x < s-1; x++ {
			c := at(x, y)
			if at(x+1, y) == c && at(x, y+1) == c && at(x+1, y+1) == c {
				p += 3
			}
		}
	}
	dark := 0
	for _, d := range m.dark {
		if d {
			dark++
		}
	}
	total := s * s
	k := (abs(dark*20-total*10)+total-1)/total - 1
	return p + 10*k
}

// line scores one row or column: 3 for a run of five alike, and 1 for
// each module more; 40 for each dark-light-dark-dark-dark-light-dark
// with four light modules, or the edge, on one side.
func line(s int, at func(int) bool) int {
	p, run := 0, 1
	for i := 1; i <= s; i++ {
		if i < s && at(i) == at(i-1) {
			run++
			continue
		}
		if run >= 5 {
			p += 3 + run - 5
		}
		run = 1
	}
	finder := [7]bool{true, false, true, true, true, false, true}
	light := func(from, to int) bool {
		for k := from; k < to; k++ {
			if k >= 0 && k < s && at(k) {
				return false
			}
		}
		return true
	}
	for i := 0; i+7 <= s; i++ {
		alike := true
		for j, d := range finder {
			if at(i+j) != d {
				alike = false
				break
			}
		}
		if alike && (light(i-4, i) || light(i+7, i+11)) {
			p += 40
		}
	}
	return p
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
