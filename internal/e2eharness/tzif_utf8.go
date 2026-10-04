package e2eharness

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WriteTZifUTF8Fixture exercises the binary-to-text boundary independently of
// the host's zoneinfo installation. Both transition-table formats are covered.
func WriteTZifUTF8Fixture(t testing.TB) string {
	t.Helper()
	type fixture struct {
		name, want, rule string
		data             []byte
		valid            bool
	}
	block := func(version byte, width int, names []byte, first, second byte) []byte {
		out := make([]byte, 44)
		copy(out, "TZif")
		out[4] = version
		binary.BigEndian.PutUint32(out[32:], 2)
		binary.BigEndian.PutUint32(out[36:], 2)
		binary.BigEndian.PutUint32(out[40:], uint32(len(names)))
		for _, instant := range []uint64{1000, 2000} {
			if width == 4 {
				out = binary.BigEndian.AppendUint32(out, uint32(instant))
			} else {
				out = binary.BigEndian.AppendUint64(out, instant)
			}
		}
		out = append(out, 1, 0)
		out = binary.BigEndian.AppendUint32(out, 3600)
		out = append(out, 0, first)
		out = binary.BigEndian.AppendUint32(out, 7200)
		out = append(out, 1, second)
		return append(out, names...)
	}
	file := func(v2 bool, names []byte, first, second byte, footer []byte) []byte {
		if !v2 {
			return block(0, 4, names, first, second)
		}
		out := block('2', 4, []byte("OLD\x00"), 0, 0)
		out = append(out, block('2', 8, names, first, second)...)
		return append(out, footer...)
	}
	var cases []fixture
	bad := [][]byte{
		{0x80}, {0xff}, {0xc0, 0x80}, {0xc1, 0xbf}, {0xc2},
		{0xe0, 0x80, 0x80}, {0xe2, 0x82}, {0xed, 0xa0, 0x80},
		{0xf0, 0x80, 0x80, 0x80}, {0xf0, 0x9f, 0x98},
		{0xf4, 0x90, 0x80, 0x80}, {0xf5, 0x80, 0x80, 0x80}, {0xc2, 'A'},
	}
	for _, v2 := range []bool{false, true} {
		for _, name := range []string{"", "UTC", "é", "日本", "\U00010400", "\U0010ffff"} {
			cases = append(cases, fixture{fmt.Sprintf("v2=%t valid %q", v2, name), name, "", file(v2, append([]byte(name), 0), 0, 0, nil), true})
		}
		for i, bytes := range bad {
			for _, second := range []bool{false, true} {
				names := append(append([]byte("UTC\x00"), bytes...), 0)
				first, last := byte(4), byte(0)
				if second {
					first, last = 0, 4
				}
				cases = append(cases, fixture{name: fmt.Sprintf("v2=%t malformed=%d second=%t", v2, i, second), data: file(v2, names, first, last, nil)})
			}
		}
		cases = append(cases,
			fixture{fmt.Sprintf("v2=%t bounded name", v2), "UTC", "", file(v2, []byte{'U', 'T', 'C', 0, 0xff}, 0, 0, nil), true},
			fixture{fmt.Sprintf("v2=%t no terminator", v2), "é", "", file(v2, []byte("é"), 0, 0, nil), true},
			fixture{fmt.Sprintf("v2=%t outside table", v2), "", "", file(v2, []byte{0xff}, 255, 255, nil), true},
			fixture{name: fmt.Sprintf("v2=%t inside scalar", v2), data: file(v2, []byte("é\x00"), 1, 1, nil)},
		)
	}
	for i, bytes := range bad {
		footer := append(append([]byte("\n<"), bytes...), []byte(">0\n")...)
		cases = append(cases, fixture{name: fmt.Sprintf("malformed footer %d", i), data: file(true, []byte("UTC\x00"), 0, 0, footer)})
	}
	for _, footer := range []string{"\n\n", "", "not a footer\xff"} {
		cases = append(cases, fixture{"absent or empty footer", "UTC", "", file(true, []byte("UTC\x00"), 0, 0, []byte(footer)), true})
	}
	cases = append(cases,
		fixture{"Unicode footer", "UTC", "é日本", file(true, []byte("UTC\x00"), 0, 0, []byte("\n<é日本>-3\n")), true},
		fixture{"bounded footer", "UTC", "UTC", file(true, []byte("UTC\x00"), 0, 0, []byte("\nUTC0\n\xff")), true},
	)
	var source strings.Builder
	source.WriteString(`import "std/tz";
import "std/utf8";

function valid(own data: u8[], name: string, rule: string): boolean {
  let parsed: Option[tz.Zone] = tz.parse_tzif(data);
  data = [];
  match (parsed) {
    Some(zone) => {
      if (zone.first_abbr != name || zone.abbrs.len() != 2 || zone.rule.std_name != rule) { return false; }
      if (!utf8.is_valid_utf8(zone.first_abbr) || !utf8.is_valid_utf8(zone.rule.std_name) || !utf8.is_valid_utf8(zone.rule.dst_name)) { return false; }
      let i: i32 = 0;
      while (i < zone.abbrs.len()) {
        if (zone.abbrs[i] != name || !utf8.is_valid_utf8(zone.abbrs[i])) { return false; }
        i = i + 1;
      }
      return true;
    },
    None => { return false; }
  }
}

function rejected(data: u8[]): boolean {
  match (tz.parse_tzif(data)) {
    Some(_) => { return false; },
    None => { return true; }
  }
}

function main(): i32 {
`)
	for i, c := range cases {
		fmt.Fprintf(&source, "  // %d: %s\n  if (!", i+1, c.name)
		if c.valid {
			source.WriteString("valid(")
		} else {
			source.WriteString("rejected(")
		}
		source.WriteByte('[')
		for j, b := range c.data {
			if j != 0 {
				source.WriteString(", ")
			}
			fmt.Fprintf(&source, "%d as u8", b)
		}
		source.WriteByte(']')
		if c.valid {
			want, _ := json.Marshal(c.want)
			rule, _ := json.Marshal(c.rule)
			fmt.Fprintf(&source, ", %s, %s", want, rule)
		}
		fmt.Fprintf(&source, ")) { return %d; }\n", i+1)
	}
	source.WriteString("  return 0;\n}\n")
	path := filepath.Join(t.TempDir(), "tzif-utf8.fern")
	if err := os.WriteFile(path, []byte(source.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
