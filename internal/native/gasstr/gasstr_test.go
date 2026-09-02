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

func TestFileDirective(t *testing.T) {
	files, err := FileDirective(nil, []string{"1", `"a.fern"`})
	if err != nil || files[1] != "a.fern" {
		t.Fatalf("got %v, %v", files, err)
	}
	if files, err = FileDirective(files, []string{"2", `"lib/b.fern"`}); err != nil || files[2] != "lib/b.fern" {
		t.Fatalf("got %v, %v", files, err)
	}
	// The bare ELF form defines nothing and is not an error.
	if got, err := FileDirective(files, []string{`"prog.fern"`}); err != nil || len(got) != 2 {
		t.Errorf("bare .file: got %v, %v", got, err)
	}
	// Redefining a number with a different path is the kind of drift a
	// consumer cannot see.
	if _, err := FileDirective(files, []string{"1", `"other.fern"`}); err == nil {
		t.Error("redefining .file 1 was accepted")
	}
	if _, err := FileDirective(files, []string{"1", `"a.fern"`}); err != nil {
		t.Errorf("restating .file 1 identically: %v", err)
	}
	for _, bad := range [][]string{{"0", `"x"`}, {"x", `"x"`}, {"1", "x"}, {"1", `"x"`, "extra"}} {
		if _, err := FileDirective(nil, bad); err == nil {
			t.Errorf(".file %v was accepted", bad)
		}
	}
}

func TestLocDirective(t *testing.T) {
	cases := []struct {
		ops  []string
		want LineRow
	}{
		{[]string{"1", "3"}, LineRow{Offset: 7, File: 1, Line: 3, IsStmt: true}},
		{[]string{"2", "10", "5"}, LineRow{Offset: 7, File: 2, Line: 10, Col: 5, IsStmt: true}},
		{[]string{"1", "3", "9", "prologue_end"}, LineRow{Offset: 7, File: 1, Line: 3, Col: 9, PrologueEnd: true, IsStmt: true}},
		{[]string{"1", "3", "prologue_end"}, LineRow{Offset: 7, File: 1, Line: 3, PrologueEnd: true, IsStmt: true}},
		{[]string{"1", "12", "3", "is_stmt", "0"}, LineRow{Offset: 7, File: 1, Line: 12, Col: 3}},
		{[]string{"1", "12", "3", "epilogue_begin", "is_stmt", "1"}, LineRow{Offset: 7, File: 1, Line: 12, Col: 3, EpilogueBegin: true, IsStmt: true}},
	}
	for _, c := range cases {
		got, err := LocDirective(c.ops, 7, true)
		if err != nil || got != c.want {
			t.Errorf(".loc %v: got %+v, %v; want %+v", c.ops, got, err, c.want)
		}
	}
	// is_stmt is sticky: a row after `is_stmt 0` inherits it, and only an
	// explicit `is_stmt 1` restores it. gas is the oracle for this
	// (TestDebugLineMatchesGNUAs); a per-row default of true was the first
	// thing that differential found.
	if got, _ := LocDirective([]string{"1", "5"}, 0, false); got.IsStmt {
		t.Error("a .loc after `is_stmt 0` came back is_stmt")
	}
	if got, _ := LocDirective([]string{"1", "5", "is_stmt", "1"}, 0, false); !got.IsStmt {
		t.Error("`is_stmt 1` did not restore the flag")
	}
	for _, bad := range [][]string{{"1"}, {"0", "3"}, {"1", "-1"}, {"1", "3", "isa", "1"}, {"1", "3", "discriminator", "2"}, {"1", "3", "is_stmt"}, {"1", "3", "is_stmt", "2"}} {
		if _, err := LocDirective(bad, 0, true); err == nil {
			t.Errorf(".loc %v was accepted", bad)
		}
	}
}
