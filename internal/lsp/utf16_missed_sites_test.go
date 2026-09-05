package lsp

import (
	"strings"
	"testing"
)

// The #8468 conversion reached hover, definition, references, rename,
// diagnostics and symbols. Three sites it did not reach are pinned here,
// because each is wrong in a way the others' tests cannot see.

// A whole-document format edit replaces a range whose END is the last line's
// column. Counted in bytes, the client is told to replace fewer characters
// than the document has, and the tail survives the edit.
func TestFormattingRangeEndIsUTF16(t *testing.T) {
	src := "function main(): i32 {\n  var s: string = \"é→🐛\";\n  return 0;\n}\n// é→🐛"
	last := "// é→🐛"
	if got, want := utf16Len(last), len([]byte(last)); got == want {
		t.Fatalf("fixture does not exercise the conversion: %d units == %d bytes", got, want)
	}
	r := wholeDocumentRange(src)
	if r.End.Character != utf16Len(last) {
		t.Errorf("document-end column = %d, want %d (UTF-16 units of %q); a byte count is %d",
			r.End.Character, utf16Len(last), last, len(last))
	}
}

// A semantic token's START is a column, so a multi-byte character earlier on
// the line moves it. Fern identifiers are ASCII-only (the lexer rejects
// `é` in a name), so a token's own LENGTH cannot currently differ between
// bytes and units — the conversion is there so it stays right if that ever
// changes, and this pins the half that is reachable today.
func TestSemanticTokenStartIsUTF16(t *testing.T) {
	// The conversion is on a name REFERENCE, which is what this pass emits;
	// a declaration name is not tokenised.
	declLine := `  var s: string = "é→🐛"; return n;`
	src := "function main(): i32 {\n  var n: i32 = 7;\n" + declLine + "\n}\n"
	s := NewServer()
	s.updateDoc("file:///t", src)
	resp := runSemanticTokens(s.docs["file:///t"], "file:///t")
	if len(resp.Data) == 0 {
		t.Fatal("no semantic tokens emitted")
	}
	byteCol := strings.LastIndex(declLine, "n") + 1
	wantChar := utf16ColForByte(declLine, byteCol)
	if wantChar == byteCol-1 {
		t.Fatalf("fixture does not exercise the conversion: byte col %d, UTF-16 offset %d", byteCol, wantChar)
	}
	var line, char int
	for i := 0; i+4 < len(resp.Data); i += 5 {
		dl, dc := resp.Data[i], resp.Data[i+1]
		line += dl
		if dl != 0 {
			char = dc
		} else {
			char += dc
		}
		if line == 2 && char == wantChar {
			return
		}
	}
	t.Errorf("no token at line 2 UTF-16 char %d; a byte column would put it at %d",
		wantChar, byteCol-1)
}

// signatureHelp resolves the client's position itself rather than through the
// shared converter, so it needs its own row: a byte-counting scan lands short
// on a line with a multi-byte character before the cursor.
func TestSignatureHelpPositionIsUTF16(t *testing.T) {
	line := `  var n: i32 = add("é→🐛", `
	src := "function add(s: string, n: i32): i32 { return n; }\n" +
		"function main(): i32 {\n" + line + "1);\n  return n;\n}\n"
	u16 := utf16Len(line)
	if u16 == len(line) {
		t.Fatal("fixture does not exercise the conversion")
	}
	off := lspPositionToOffset(src, Position{Line: 2, Character: u16})
	if off < 0 {
		t.Fatalf("position not resolved")
	}
	// The offset must land at the end of that line's text, not short of it.
	want := len("function add(s: string, n: i32): i32 { return n; }\n"+
		"function main(): i32 {\n") + len(line)
	if off != want {
		t.Errorf("offset = %d, want %d — a byte-counting scan gives %d", off, want,
			len("function add(s: string, n: i32): i32 { return n; }\nfunction main(): i32 {\n")+u16)
	}
}
