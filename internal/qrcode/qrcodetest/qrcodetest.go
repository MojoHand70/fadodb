// Package qrcodetest reads QR codes back, for tests: the codes package
// qrcode draws, from their modules or from the SVG a page holds. It is
// written from ISO/IEC 18004 apart from the encoder, with its own
// tables and its own arithmetic in GF(256), so that a test that reads a
// code back checks the encoder against the standard and not against
// itself. It reads what the encoder makes: level M, bytes, versions 1
// to 20.
package qrcodetest

import (
	"fmt"
	"regexp"
	"strconv"
)

// code is a code as the reader sees it: its size and its modules.
type code struct {
	Size int
	dark func(x, y int) bool
}

func (c *code) Dark(x, y int) bool { return c.dark(x, y) }

// Read reads a code of size modules a side, each dark or light as dark
// says, back to its text, or says what is wrong with it.
func Read(size int, dark func(x, y int) bool) (string, error) {
	return read(&code{Size: size, dark: dark})
}

// FormatWord is the 15 bits of a code's first copy of its format
// information, as the standard reads them.
func FormatWord(size int, dark func(x, y int) bool) int {
	return formatWord(&code{Size: size, dark: dark})
}

// VersionWord is the 18 bits of a code's version information, from
// version 7.
func VersionWord(size int, dark func(x, y int) bool) int {
	return versionWord(&code{Size: size, dark: dark})
}

// ReadSVG reads the code of a picture package qrcode drew: its viewBox,
// with the quiet zone of four modules, and one rectangle a run of dark
// modules.
func ReadSVG(svg string) (string, error) {
	box := regexp.MustCompile(`viewBox="0 0 (\d+) (\d+)"`).FindStringSubmatch(svg)
	if box == nil {
		return "", fmt.Errorf("the picture has no viewBox")
	}
	n, _ := strconv.Atoi(box[1])
	size := n - 8
	if size < 21 {
		return "", fmt.Errorf("a picture of %d modules", n)
	}
	modules := make([]bool, size*size)
	for _, m := range regexp.MustCompile(`M(\d+) (\d+)h(\d+)v1h-(\d+)z`).FindAllStringSubmatch(svg, -1) {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		w, _ := strconv.Atoi(m[3])
		for i := 0; i < w; i++ {
			if x-4+i < 0 || x-4+i >= size || y-4 < 0 || y-4 >= size {
				return "", fmt.Errorf("a module outside the code at %d,%d", x+i, y)
			}
			modules[(y-4)*size+x-4+i] = true
		}
	}
	return Read(size, func(x, y int) bool { return modules[y*size+x] })
}

// alignmentTable is the standard's table of the alignment patterns'
// rows and columns, versions 2 to 20.
func alignmentTable(v int) []int {
	return map[int][]int{
		2: {6, 18}, 3: {6, 22}, 4: {6, 26}, 5: {6, 30}, 6: {6, 34}, 7: {6, 22, 38}, 8: {6, 24, 42},
		9: {6, 26, 46}, 10: {6, 28, 50}, 11: {6, 30, 54}, 12: {6, 32, 58}, 13: {6, 34, 62},
		14: {6, 26, 46, 66}, 15: {6, 26, 48, 70}, 16: {6, 26, 50, 74}, 17: {6, 30, 54, 78},
		18: {6, 30, 56, 82}, 19: {6, 30, 58, 86}, 20: {6, 34, 62, 90},
	}[v]
}

// blockTable is the standard's table 9 at level M: Reed-Solomon
// codewords per block, then the blocks of each group and their data
// codewords.
func blockTable(v int) (ec int, groups [][2]int) {
	rows := map[int][5]int{
		1: {10, 1, 16, 0, 0}, 2: {16, 1, 28, 0, 0}, 3: {26, 1, 44, 0, 0}, 4: {18, 2, 32, 0, 0},
		5: {24, 2, 43, 0, 0}, 6: {16, 4, 27, 0, 0}, 7: {18, 4, 31, 0, 0}, 8: {22, 2, 38, 2, 39},
		9: {22, 3, 36, 2, 37}, 10: {26, 4, 43, 1, 44}, 11: {30, 1, 50, 4, 51}, 12: {22, 6, 36, 2, 37},
		13: {22, 8, 37, 1, 38}, 14: {24, 4, 40, 5, 41}, 15: {24, 5, 41, 5, 42}, 16: {28, 7, 45, 3, 46},
		17: {28, 10, 46, 1, 47}, 18: {26, 9, 43, 4, 44}, 19: {26, 3, 44, 11, 45}, 20: {26, 3, 41, 13, 42},
	}
	r := rows[v]
	return r[0], [][2]int{{r[1], r[2]}, {r[3], r[4]}}
}

// readFormat reads the first copy of the format information: row 8
// from the left, then column 8 upwards, past the timing lines.
func formatWord(c *code) int {
	w := 0
	take := func(x, y int) {
		w <<= 1
		if c.Dark(x, y) {
			w |= 1
		}
	}
	for x := 0; x <= 5; x++ {
		take(x, 8)
	}
	take(7, 8)
	take(8, 8)
	take(8, 7)
	for y := 5; y >= 0; y-- {
		take(8, y)
	}
	return w
}

// readFormat2 reads the second copy: column 8 upwards from the bottom,
// then row 8 from 8 modules before the right edge.
func formatWord2(c *code) int {
	w := 0
	for y := c.Size - 1; y >= c.Size-7; y-- {
		w <<= 1
		if c.Dark(8, y) {
			w |= 1
		}
	}
	for x := c.Size - 8; x < c.Size; x++ {
		w <<= 1
		if c.Dark(x, 8) {
			w |= 1
		}
	}
	return w
}

// readVersion reads the version information above the bottom-left
// finder: the highest bit at the bottom right of its block.
func versionWord(c *code) int {
	w := 0
	for x := 5; x >= 0; x-- {
		for y := c.Size - 9; y >= c.Size-11; y-- {
			w <<= 1
			if c.Dark(x, y) {
				w |= 1
			}
		}
	}
	return w
}

// read reads a code of level M back to its text, or says what is wrong.
func read(c *code) (string, error) {
	s := c.Size
	v := (s - 17) / 4
	if s < 21 || (s-17)%4 != 0 || v > 40 {
		return "", fmt.Errorf("a size of %d", s)
	}
	// The finders, their borders, the timing lines and the dark module.
	for _, f := range [][2]int{{0, 0}, {s - 7, 0}, {0, s - 7}} {
		for dy := -1; dy <= 7; dy++ {
			for dx := -1; dx <= 7; dx++ {
				x, y := f[0]+dx, f[1]+dy
				if x < 0 || y < 0 || x >= s || y >= s {
					continue
				}
				ring := min(min(dx, dy), min(6-dx, 6-dy))
				if want := ring == 0 || ring >= 2; c.Dark(x, y) != want {
					return "", fmt.Errorf("the finder at %v is wrong at %d,%d", f, x, y)
				}
			}
		}
	}
	for i := 8; i < s-8; i++ {
		if c.Dark(i, 6) != (i%2 == 0) || c.Dark(6, i) != (i%2 == 0) {
			return "", fmt.Errorf("the timing lines are wrong at %d", i)
		}
	}
	if !c.Dark(8, s-8) {
		return "", fmt.Errorf("no dark module")
	}
	// The format: level M and a mask, the same in both copies.
	f := formatWord(c)
	if f2 := formatWord2(c); f2 != f {
		return "", fmt.Errorf("the copies of the format differ: %015b and %015b", f, f2)
	}
	mask := -1
	for k, w := range []int{0x5412, 0x5125, 0x5E7C, 0x5B4B, 0x45F9, 0x40CE, 0x4F97, 0x4AA0} {
		if w == f {
			mask = k
		}
	}
	if mask < 0 {
		return "", fmt.Errorf("the format %015b is not level M", f)
	}
	if v >= 7 {
		if got := versionWord(c); got>>12 != v {
			return "", fmt.Errorf("the version information says %d", got>>12)
		}
	}
	// What is not data: the finders with their borders and the format,
	// the timing lines, the alignment patterns, the version information.
	reserved := make([]bool, s*s)
	mark := func(x, y int) { reserved[y*s+x] = true }
	for y := 0; y < s; y++ {
		for x := 0; x < s; x++ {
			if (x <= 8 && y <= 8) || (x >= s-8 && y <= 8) || (x <= 8 && y >= s-8) || x == 6 || y == 6 {
				mark(x, y)
			}
			if v >= 7 && ((x >= s-11 && x <= s-9 && y <= 5) || (y >= s-11 && y <= s-9 && x <= 5)) {
				mark(x, y)
			}
		}
	}
	at := alignmentTable(v)
	for _, ay := range at {
		for _, ax := range at {
			if (ax <= 8 && ay <= 8) || (ax >= s-9 && ay <= 8) || (ax <= 8 && ay >= s-9) {
				continue
			}
			for y := ay - 2; y <= ay+2; y++ {
				for x := ax - 2; x <= ax+2; x++ {
					mark(x, y)
				}
			}
		}
	}
	// The bits, in pairs of columns from the right, up and down by turns,
	// each unmasked.
	unmask := map[int]func(x, y int) bool{
		0: func(x, y int) bool { return (y+x)%2 == 0 },
		1: func(x, y int) bool { return y%2 == 0 },
		2: func(x, y int) bool { return x%3 == 0 },
		3: func(x, y int) bool { return (y+x)%3 == 0 },
		4: func(x, y int) bool { return (y/2+x/3)%2 == 0 },
		5: func(x, y int) bool { return (y*x)%2+(y*x)%3 == 0 },
		6: func(x, y int) bool { return ((y*x)%2+(y*x)%3)%2 == 0 },
		7: func(x, y int) bool { return ((y+x)%2+(y*x)%3)%2 == 0 },
	}[mask]
	var stream []bool
	up := true
	for col := s - 1; col > 0; col -= 2 {
		if col == 6 {
			col--
		}
		for i := 0; i < s; i++ {
			y := i
			if up {
				y = s - 1 - i
			}
			for _, x := range []int{col, col - 1} {
				if !reserved[y*s+x] {
					stream = append(stream, c.Dark(x, y) != unmask(x, y))
				}
			}
		}
		up = !up
	}
	words := make([]byte, len(stream)/8)
	for i := range words {
		for j := 0; j < 8; j++ {
			if stream[8*i+j] {
				words[i] |= 0x80 >> j
			}
		}
	}
	// The blocks, taken apart again, each checked by its syndromes.
	ecCount, groups := blockTable(v)
	var sizes []int
	for _, g := range groups {
		for i := 0; i < g[0]; i++ {
			sizes = append(sizes, g[1])
		}
	}
	if total := sum(sizes) + ecCount*len(sizes); total != len(words) {
		return "", fmt.Errorf("%d codewords read, %d in the table", len(words), total)
	}
	blocks := make([][]byte, len(sizes))
	k := 0
	for i := 0; i < sizes[len(sizes)-1]; i++ {
		for b, n := range sizes {
			if i < n {
				blocks[b] = append(blocks[b], words[k])
				k++
			}
		}
	}
	for i := 0; i < ecCount; i++ {
		for b := range blocks {
			blocks[b] = append(blocks[b], words[k])
			k++
		}
	}
	exp, log := tables()
	var data []byte
	for b, blk := range blocks {
		for i := 0; i < ecCount; i++ {
			var syn byte
			for j, w := range blk {
				if w != 0 {
					syn ^= exp[(log[w]+i*(len(blk)-1-j))%255]
				}
			}
			if syn != 0 {
				return "", fmt.Errorf("block %d: syndrome %d is %d", b, i, syn)
			}
		}
		data = append(data, blk[:sizes[b]]...)
	}
	// The text: the mode of bytes, its count, its bytes.
	pos := 0
	next := func(n int) int {
		x := 0
		for i := 0; i < n; i++ {
			x <<= 1
			if data[(pos+i)/8]&(0x80>>((pos+i)%8)) != 0 {
				x |= 1
			}
		}
		pos += n
		return x
	}
	if m := next(4); m != 0b0100 {
		return "", fmt.Errorf("mode %04b", m)
	}
	n := next(map[bool]int{true: 8, false: 16}[v <= 9])
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(next(8))
	}
	return string(out), nil
}

// tables are GF(256)'s powers of 2 and their logarithms.
func tables() (exp [255]byte, log [256]int) {
	x := 1
	for i := 0; i < 255; i++ {
		exp[i] = byte(x)
		log[x] = i
		x <<= 1
		if x >= 256 {
			x ^= 0x11D
		}
	}
	return exp, log
}

func sum(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}
