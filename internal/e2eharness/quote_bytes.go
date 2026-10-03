package e2eharness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WriteQuoteBytesFixture imports the actual GNU diagnostic module.
func WriteQuoteBytesFixture(t testing.TB) string {
	t.Helper()
	lib, err := os.ReadFile(filepath.Join("..", "..", "coreutils", "lib", "gnu.fern"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gnu.fern"), lib, 0o644); err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	want.WriteByte('\'')
	for b := 0; b < 256; b++ {
		switch b {
		case 7:
			want.WriteString(`\a`)
		case 8:
			want.WriteString(`\b`)
		case 9:
			want.WriteString(`\t`)
		case 10:
			want.WriteString(`\n`)
		case 11:
			want.WriteString(`\v`)
		case 12:
			want.WriteString(`\f`)
		case 13:
			want.WriteString(`\r`)
		case 39, 92:
			want.WriteByte('\\')
			want.WriteByte(byte(b))
		default:
			if b >= 32 && b < 127 {
				want.WriteByte(byte(b))
			} else {
				fmt.Fprintf(&want, `\%03o`, b)
			}
		}
	}
	want.WriteByte('\'')
	source := "import \"./gnu\";\nimport \"std/string\";\n" + fmt.Sprintf(`
function main(): i32 {
  let input: u8[] = [];
  let i: i32 = 0;
  while (i < 256) { input = input.append(i as u8); i = i + 1; }
  let held: u8[] = input;
  let rendered: string = gnu.quote_bytes(input);
  if (rendered != %q) { return 1; }
  input = input.with(0, 42 as u8);
  if (gnu.quote_bytes(held) != rendered || held[0] != 0 as u8) { return 2; }
  if (gnu.quote_bytes([]) != "''") { return 3; }
  if (gnu.quote_bytes([39 as u8, 92 as u8, 10 as u8, 255 as u8]) != %q) { return 4; }
  let text: string = "hello 'world'\n";
  if (gnu.quote_bytes(text.bytes()) != gnu.quote(text)) { return 5; }
  return 0;
}
`, want.String(), `'\'\\\n\377'`)
	path := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
