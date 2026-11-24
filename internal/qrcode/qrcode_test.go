package qrcode

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/qrcode/qrcodetest"
)

// The Reed-Solomon codewords of two blocks published with the standard:
// "01234567" in version 1-M, the example of ISO/IEC 18004, annex I, and
// "HELLO WORLD" in version 1-M, the example most tutorials work.
func TestTheBlocksOfTheStandard(t *testing.T) {
	for _, c := range []struct {
		name     string
		data, ec []byte
	}{
		{"01234567", []byte{16, 32, 12, 86, 97, 128, 236, 17, 236, 17, 236, 17, 236, 17, 236, 17},
			[]byte{165, 36, 212, 193, 237, 54, 199, 135, 44, 85}},
		{"HELLO WORLD", []byte{32, 91, 11, 120, 209, 114, 220, 77, 67, 64, 236, 17, 236, 17, 236, 17},
			[]byte{196, 35, 39, 119, 235, 215, 231, 226, 93, 23}},
	} {
		if got := remainder(c.data, generator(10)); !bytes.Equal(got, c.ec) {
			t.Errorf("%s: %v, want %v", c.name, got, c.ec)
		}
	}
}

// The format information of level M for each mask, and the version
// information of versions 7 to 20, as the standard's tables give them.
func TestTheFormatAndVersionWords(t *testing.T) {
	formats := []int{0x5412, 0x5125, 0x5E7C, 0x5B4B, 0x45F9, 0x40CE, 0x4F97, 0x4AA0}
	for mask, want := range formats {
		m := newMatrix(1)
		m.format(mask)
		if got := qrcodetest.FormatWord(m.size, m.at); got != want {
			t.Errorf("mask %d: format %015b, want %015b", mask, got, want)
		}
	}
	versions := map[int]int{7: 0x07C94, 8: 0x085BC, 9: 0x09A99, 10: 0x0A4D3, 11: 0x0BBF6, 12: 0x0C762, 13: 0x0D847,
		14: 0x0E60D, 15: 0x0F928, 16: 0x10B78, 17: 0x1145D, 18: 0x12A17, 19: 0x13532, 20: 0x149A6}
	for v, want := range versions {
		m := newMatrix(v)
		if got := qrcodetest.VersionWord(m.size, m.at); got != want {
			t.Errorf("version %d: %018b, want %018b", v, got, want)
		}
	}
}

// The table of blocks holds for every version: its data and its
// Reed-Solomon codewords fill the modules the version leaves free for
// data, with the remainder bits the standard gives it, and its
// alignment patterns stand where the standard's table puts them.
func TestTheTableOfBlocks(t *testing.T) {
	remainderBits := func(v int) int {
		switch {
		case v >= 2 && v <= 6:
			return 7
		case v >= 14 && v <= 20:
			return 3
		}
		return 0
	}
	for v := 1; v <= MaxVersion; v++ {
		free := 0
		for _, f := range newMatrix(v).fixed {
			if !f {
				free++
			}
		}
		b := blocksOf(v)
		total := dataCodewords(v) + b.ec*(b.n1+b.n2)
		if free != 8*total+remainderBits(v) {
			t.Errorf("version %d: %d modules free, and %d codewords and %d remainder bits", v, free, total, remainderBits(v))
		}
		if got, want := fmt.Sprint(alignments(v)), fmt.Sprint(alignmentTable(v)); got != want {
			t.Errorf("version %d: alignment patterns at %s, want %s", v, got, want)
		}
	}
}

// Every length of text, from one byte to the most a version 20 holds,
// makes a code that a reader written apart from the encoder reads back:
// its patterns, its format and version, the syndromes of each block, and
// the text. Each version is reached at the length where the one before
// is full.
func TestEveryCodeReadsBack(t *testing.T) {
	for n := 1; n <= 666; n++ {
		if n > 60 && n%7 != 0 && versionFor(n) == versionFor(n-1) {
			continue
		}
		text := strings.Repeat("https://fado.acme.example/auth/phone?t=fadop_", 20)[:n]
		c, err := Encode(text)
		if err != nil {
			t.Fatalf("%d bytes: %v", n, err)
		}
		if got, err := qrcodetest.Read(c.Size, c.Dark); err != nil || got != text {
			t.Fatalf("%d bytes, version %d: read %q, %v", n, (c.Size-17)/4, got, err)
		}
	}
	if _, err := Encode(strings.Repeat("x", 667)); err == nil {
		t.Error("667 bytes made a code")
	}
}

// Each of the eight masks reads back, in a version with no version
// information and in one with it.
func TestEachMaskReadsBack(t *testing.T) {
	for _, text := range []string{"https://amber-ballad-27.fadodb.app/auth/phone?t=fadop_7GQ2M4XK0P3ZD9A1B2C3D4E5F6G7H8J9K0", strings.Repeat("Ą€", 70)} {
		v := versionFor(len(text))
		for mask := 0; mask < 8; mask++ {
			c := build(v, []byte(text), mask)
			got, err := qrcodetest.Read(c.Size, c.Dark)
			if err != nil || got != text {
				t.Errorf("version %d, mask %d: read %q, %v", v, mask, got, err)
			}
		}
	}
}

// The smallest version is chosen: 14 bytes fit version 1, 15 do not.
func TestTheSmallestVersion(t *testing.T) {
	for n, want := range map[int]int{1: 1, 14: 1, 15: 2, 26: 2, 27: 3, 106: 6, 107: 7, 666: 20} {
		c, err := Encode(strings.Repeat("a", n))
		if err != nil || (c.Size-17)/4 != want {
			t.Errorf("%d bytes: version %d, %v; want %d", n, (c.Size-17)/4, err, want)
		}
	}
}

// The picture holds the quiet zone and one rectangle a run of dark
// modules, and the label it is given.
func TestThePicture(t *testing.T) {
	c, _ := Encode("https://fado.acme.example")
	svg := c.SVG("A code for your phone")
	n := c.Size + 8
	for _, part := range []string{fmt.Sprintf(`viewBox="0 0 %d %d"`, n, n), `aria-label="A code for your phone"`, `fill="#fff"`, `<path fill="#000" d="M4 4h7v1h-7z`} {
		if !strings.Contains(svg, part) {
			t.Errorf("the picture lacks %s: %s", part, svg[:200])
		}
	}
}

// The picture a page holds reads back to its text.
func TestThePictureReadsBack(t *testing.T) {
	link := "https://amber-ballad-27.fadodb.app/auth/phone?t=fadop_abcdefghijklmnopqrstuvwxyz234567"
	c, _ := Encode(link)
	if got, err := qrcodetest.ReadSVG(c.SVG("x")); err != nil || got != link {
		t.Errorf("read %q, %v", got, err)
	}
}

// at reports a module of a matrix being made, for the reader.
func (m *matrix) at(x, y int) bool { return m.dark[y*m.size+x] }

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
