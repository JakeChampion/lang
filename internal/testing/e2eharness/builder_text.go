package e2eharness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WriteBuilderTextFixture checks text extraction independently of the
// decoder implementation, including reuse and the raw byte sibling.
func WriteBuilderTextFixture(t testing.TB) string {
	t.Helper()
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, ""},
		{"ASCII and NUL", []byte("a\x00b"), "a\x00b"},
		{"all scalar widths", []byte("¢€𐐀\U0010ffff"), "¢€𐐀\U0010ffff"},
		{"stray continuation", []byte{0x80}, "�"},
		{"invalid lead", []byte{0xff}, "�"},
		{"overlong two", []byte{0xc0, 0xaf}, "��"},
		{"overlong three", []byte{0xe0, 0x80, 0xaf}, "���"},
		{"overlong four", []byte{0xf0, 0x80, 0x80, 0xaf}, "����"},
		{"surrogate", []byte{0xed, 0xa0, 0x80}, "���"},
		{"above Unicode", []byte{0xf4, 0x90, 0x80, 0x80}, "����"},
		{"invalid four lead", []byte{0xf5, 0x80, 0x80, 0x80}, "����"},
		{"truncated two", []byte{0xc2}, "�"},
		{"truncated three", []byte{0xe2, 0x82}, "�"},
		{"truncated four", []byte{0xf0, 0x90, 0x80}, "�"},
		{"interrupted two", []byte{0xc2, 'A'}, "�A"},
		{"interrupted three", []byte{0xe2, 0x82, 'A'}, "�A"},
		{"interrupted four", []byte{0xf0, 0x90, 0x80, 'A'}, "�A"},
		{"separate maximal subparts", []byte{0x80, 0xe2, 0x82}, "��"},
		{"adjacent valid text", []byte("¢\xe2\x82A€\xff𐐀"), "¢�A€�𐐀"},
		{"growing valid buffer", []byte(strings.Repeat("¢€𐐀", 513)), strings.Repeat("¢€𐐀", 513)},
		{"growing replacement output", []byte(strings.Repeat("¢𐐀\xff\xe2\x82", 513)), strings.Repeat("¢𐐀��", 513)},
	}
	var source strings.Builder
	quote := func(s string) string {
		escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\x00", "\\0", "\n", "\\n", "\r", "\\r", "\t", "\\t").Replace(s)
		return "\"" + escaped + "\""
	}
	source.WriteString(`import "std/utf8";

function check(bytes: u8[], want: string): boolean {
  let h: usize = buf_new(1);
  for byte in bytes { buf_push_byte(h, byte as i32); }
  let first: string = buf_take(h);
  if (buf_len(h) != 0) { buf_free(h); return false; }
  buf_push_bytes_range(h, bytes, 0, bytes.len());
  let second: string = buf_take(h);
  buf_push_bytes_range(h, bytes, 0, bytes.len());
  let raw: u8[] = buf_take_bytes(h);
  if (buf_len(h) != 0) { buf_free(h); return false; }
  buf_push(h, "again");
  let reused: string = buf_take(h);
  let empty: string = buf_take(h);
  buf_free(h);
  if (first != want || second != want || reused != "again" || empty != "") { return false; }
  if (!utf8.is_valid_utf8(first) || !utf8.is_valid_utf8(second)) { return false; }
  if (raw.len() != bytes.len()) { return false; }
  let i: i32 = 0;
  while (i < raw.len()) {
    if (raw[i] != bytes[i]) { return false; }
    i = i + 1;
  }
  return true;
}

function special(): boolean {
  let h: usize = buf_new(0);
  buf_push_range(h, "a€z", 1, 2);
  buf_push_range(h, "a€z", 2, 4);
  let joined: string = buf_take(h);
  buf_push_range(h, "a€z", 2, 4);
  let cut: string = buf_take(h);
  buf_push_u64(h, 0xffffffffffffffff as u64);
  let integer: string = buf_take(h);
  buf_push_mapped(h, "\x00A", [128 as u8]);
  let mapped: string = buf_take(h);
  buf_push_bytes_mapped(h, [0 as u8, 65 as u8], [128 as u8]);
  let raw_mapped: string = buf_take(h);
  let drop: u8[] = [];
  let i: i32 = 0;
  while (i < 194) { drop = drop.append(0 as u8); i = i + 1; }
  drop = drop.append(1 as u8);
  buf_push_filtered(h, "¢", drop);
  let filtered: string = buf_take(h);
  buf_push_bytes_filtered(h, [194 as u8, 162 as u8], drop);
  let raw_filtered: string = buf_take(h);
  let expand: u8[] = [2 as u8, 194 as u8, 65 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8];
  buf_push_expanded(h, "\x00", expand);
  let expanded: string = buf_take(h);
  buf_push_bytes_expanded(h, [0 as u8], expand);
  let raw_expanded: string = buf_take(h);
  buf_free(h);
  return joined == "€" && cut == "��" && integer == "��������"
    && mapped == "�A" && raw_mapped == "�A"
    && filtered == "�" && raw_filtered == "�"
    && expanded == "�A" && raw_expanded == "�A";
}

function main(): i32 {
`)
	for _, tc := range cases {
		fmt.Fprint(&source, "  if (!check([")
		for i, b := range tc.data {
			if i != 0 {
				source.WriteString(", ")
			}
			fmt.Fprintf(&source, "%d as u8", b)
		}
		fmt.Fprintf(&source, "], %s)) { print(%s); return 1; }\n", quote(tc.want), quote(tc.name))
	}
	source.WriteString("  if (!special()) { print(\"range and transform appends\"); return 2; }\n  return 0;\n}\n")
	path := filepath.Join(t.TempDir(), "builder-text.fern")
	if err := os.WriteFile(path, []byte(source.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
