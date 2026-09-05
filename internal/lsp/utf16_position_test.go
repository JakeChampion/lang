package lsp

import (
	"strings"
	"testing"
)

// LSP measures a Position's Character in UTF-16 code units; Fern measures
// columns in UTF-8 bytes. This server passed byte offsets straight through in
// both directions, so any non-ASCII character earlier on the line shifted
// everything after it (#8468) — hover targeted the wrong token, definition
// landed mid-identifier, and a diagnostic underlined the wrong span.
//
// The three widths that matter, and what each costs if unconverted:
//
//	é  2 bytes, 1 UTF-16 unit  → 1 unit of drift
//	→  3 bytes, 1 UTF-16 unit  → 2 units of drift
//	🐛 4 bytes, 2 UTF-16 units → 2 units of drift
//
// Not exotic input here: the stdlib's own comments are full of box-drawing and
// arrow characters, and so are the diagnostics this server surfaces.

func TestUTF16LenCountsCodeUnits(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"é", 1},     // 2 bytes, one BMP rune
		{"→", 1},     // 3 bytes, one BMP rune
		{"🐛", 2},     // 4 bytes, a surrogate PAIR
		{"a→b🐛c", 6}, // 1 + 1 + 1 + 2 + 1
		{"日本語", 3},   // 3 bytes each, all BMP
	}
	for _, c := range cases {
		if got := utf16Len(c.s); got != c.want {
			t.Errorf("utf16Len(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

// The two converters must round-trip: every byte column that starts a rune
// maps to a UTF-16 offset and back to itself.
func TestPositionConvertersRoundTrip(t *testing.T) {
	for _, line := range []string{
		"var x: i32 = 7;",
		"var é: i32 = 7;",
		"// → arrow then code",
		"var s: string = \"🐛\";",
		"日本語 mixed with ascii",
	} {
		for b := range line {
			if b > 0 && !startsRune(line, b) {
				continue // a continuation byte is not a valid column
			}
			col := b + 1 // 1-based byte column
			u16 := utf16ColForByte(line, col)
			if back := byteColForUTF16(line, u16); back != col {
				t.Errorf("line %q: byte col %d → u16 %d → byte col %d, want %d", line, col, u16, back, col)
			}
		}
	}
}

func startsRune(s string, i int) bool { return s[i]&0xC0 != 0x80 }

// A line's UTF-16 offset must differ from its byte column exactly where a
// multi-byte rune precedes it — the property the old pass-through violated.
func TestUTF16OffsetDivergesFromByteColumn(t *testing.T) {
	line := "var é = 1;" // `é` occupies bytes 4..5, one UTF-16 unit
	// `=` is at byte offset 7 (1-based column 8) but UTF-16 offset 6.
	if got := line[7]; got != '=' {
		t.Fatalf("fixture drifted: line[7] = %q, want '='", got)
	}
	if got := utf16ColForByte(line, 8); got != 6 {
		t.Errorf("utf16ColForByte(%q, 8) = %d, want 6", line, got)
	}
	if got := byteColForUTF16(line, 6); got != 8 {
		t.Errorf("byteColForUTF16(%q, 6) = %d, want 8", line, got)
	}
}

func TestLineTextAt(t *testing.T) {
	src := "one\ntwo\nthree"
	for i, want := range []string{"one", "two", "three"} {
		if got := lineTextAt(src, i+1); got != want {
			t.Errorf("lineTextAt(line %d) = %q, want %q", i+1, got, want)
		}
	}
	if got := lineTextAt(src, 9); got != "" {
		t.Errorf("out-of-range line = %q, want empty", got)
	}
	if got := lineTextAt("a\r\nb", 1); got != "a" {
		t.Errorf("CRLF line = %q, want %q", got, "a")
	}
}

// Hover on a target that FOLLOWS a multi-byte character on the same line.
//
// That ordering is the whole bug: everything after the rune shifts, so a
// client's UTF-16 offset lands short of the token when read as a byte column.
// Each case asserts the fixture actually diverges before using it — a
// same-line-but-ASCII fixture would pass whether or not the conversion works,
// which is what a first draft of this test did.
func TestHoverAfterAMultiByteCharacterOnTheSameLine(t *testing.T) {
	cases := []struct {
		name string
		lit  string
	}{
		{"two_byte", `"é"`},
		{"three_byte", `"→"`},
		{"four_byte", `"🐛"`},
		{"several", `"é→🐛"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line := "  return f(" + c.lit + ", x);"
			src := "function f(s: string, n: i32): i32 { return n; }\n" +
				"function main(): i32 {\n" +
				"  var x: i32 = 7;\n" +
				line + "\n}\n"
			b := strings.LastIndex(line, "x")
			if b < 0 {
				t.Fatal("fixture has no `x`")
			}
			byteCol := b + 1 // 1-based
			u16 := utf16ColForByte(line, byteCol)
			if u16 == byteCol-1 {
				t.Fatalf("fixture does not exercise the conversion: byte col %d and UTF-16 offset %d agree", byteCol, u16)
			}
			got := hoverFor(src, 3, u16)
			if got == nil {
				t.Fatalf("no hover for `x` at UTF-16 offset %d (byte col %d) in %q", u16, byteCol, line)
			}
			if !strings.Contains(got.Contents.Value, "(var) x: i32") {
				t.Errorf("hover = %q, want it to mention (var) x: i32", got.Contents.Value)
			}
			if got.Range == nil {
				t.Fatal("hover set no range")
			}
			if got.Range.Start.Character != u16 {
				t.Errorf("range starts at %d, want %d — the range is reported back in UTF-16 units too", got.Range.Start.Character, u16)
			}
			if w := got.Range.End.Character - got.Range.Start.Character; w != 1 {
				t.Errorf("range is %d units wide for a 1-character name, want 1", w)
			}
		})
	}
}

// nameRange measures a name in UTF-16 units rather than bytes. Fern
// identifiers must be ASCII ("identifiers must be ASCII; found 'é'"), so the
// two agree for every name that can actually reach this — the change is
// defensive, not a live bug, and this pins that it did not break the ASCII
// case it is used for.
func TestNameRangeIsOneUnitPerASCIIByte(t *testing.T) {
	src := "function main(): i32 {\n  var count: i32 = 7;\n  return count;\n}\n"
	got := hoverFor(src, 2, 9) // on `count` in `return count;`
	if got == nil || got.Range == nil {
		t.Fatal("no hover range for `count`")
	}
	if w := got.Range.End.Character - got.Range.Start.Character; w != len("count") {
		t.Errorf("range is %d units wide, want %d", w, len("count"))
	}
}
