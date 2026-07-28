package gasstr

import "testing"

func TestUnquote(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want []byte
	}{
		{"ascii", `"hi"`, []byte("hi")},
		{"empty", `""`, nil},
		{"named-escapes", `"a\nb\tc\rd\\e\"f"`, []byte("a\nb\tc\rd\\e\"f")},
		{"octal", `"\000\101\200\377"`, []byte{0, 65, 0x80, 0xff}},
		{"octal-short", `"\0a"`, []byte{0, 'a'}},
		{"hex", `"\x00\x41\x80\xff"`, []byte{0, 65, 0x80, 0xff}},
		// The regression this package exists for: the emitters write bytes
		// >= 0x80 into the .asciz operand RAW, and strconv.Unquote turns each
		// of them into U+FFFD's three bytes (ef bf bd) without erroring —
		// silently corrupting the data and desynchronising it from the
		// .4byte length emitted beside it.
		{"raw-high-bytes", "\"\x80\xef\xff\"", []byte{0x80, 0xef, 0xff}},
		{"raw-high-bytes-mixed", "\"a\x80b\"", []byte{'a', 0x80, 'b'}},
		// Valid UTF-8 in a literal must survive byte-for-byte too — it is
		// just bytes here, with no decode step to round-trip through.
		{"utf8", `"héllo"`, []byte("héllo")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Unquote(tc.in)
			if err != nil {
				t.Fatalf("Unquote(%q): %v", tc.in, err)
			}
			if string(got) != string(tc.want) {
				t.Errorf("Unquote(%q) = % x, want % x", tc.in, got, tc.want)
			}
		})
	}
}

func TestUnquoteErrors(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"unquoted", `hi`},
		{"open", `"hi`},
		{"trailing-backslash", `"hi\`},
		{"unknown-escape", `"\q"`},
		{"hex-no-digits", `"\xz"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := Unquote(tc.in); err == nil {
				t.Errorf("Unquote(%q) = %q, want an error", tc.in, got)
			}
		})
	}
}
