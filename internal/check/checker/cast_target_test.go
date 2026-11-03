package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestUnknownCastTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr string
	}{
		{"cast", "5 as Wibble"},
		{"retired integer", "5 as i8"},
		{"pointer source", "(5 as usize) as Wibble"},
		{"array element", "(5 as usize) as Wibble[]"},
		{"generic argument", "(5 as usize) as Option[Wibble]"},
		{"checked cast", "5 as? Wibble"},
		{"checked retired integer", "5 as? i8"},
		{"checked generic argument", "5 as? Option[Wibble]"},
		{"unimported qualifier", "5 as missing.Item"},
		{"nested lambda", "(() => { let v = 5 as Wibble; return 0; })"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "function main(): i32 {\n  let value = " + tc.expr + ";\n  return 0;\n}\n"
			err := checkSource(t, src)
			if err == nil {
				t.Fatal("unknown cast target was accepted")
			}
			ds := flattenErrors(err)
			if len(ds) != 1 || firstErrCode(err) != "E064" {
				t.Fatalf("want one E064, got: %v", err)
			}
			pos := ds[0].(diag.Positioned).Position()
			col := strings.LastIndex(strings.Split(src, "\n")[1], " as") + 2
			if pos.Line != 2 || pos.Col != col {
				t.Fatalf("diagnostic at %v, want 2:%d", pos, col)
			}
		})
	}
}

func TestKnownCastTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		code string
	}{
		{"numeric", `function main(): i32 { let v = 5 as i64; return v as i32; }`, ""},
		{"checked numeric", `function main(): i32 { let v = 5 as? u8; return 0; }`, ""},
		{"declared pointer", `struct Item { n: i32 } function main(): i32 { let p = (0 as usize) as Item; return 0; }`, ""},
		{"type parameter", `function same[T](v: T): i32 { let value = v as T; return 0; } function main(): i32 { return 0; }`, "E033"},
		{"lambda type parameter", `function same[T](v: T): i32 { let f = (() => v as T); return 0; } function main(): i32 { return 0; }`, "E033"},
		{"pointer width", `struct Item { n: i32 } function main(): i32 { let p = 5 as Item; return 0; }`, "E069"},
		{"checked noninteger", `struct Item { n: i32 } function main(): i32 { let v = 5 as? Item; return 0; }`, "E060"},
		{"invalid conversion", `function main(): i32 { let v = true as i32; return 0; }`, "E033"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, tc.src)
			if tc.code == "" && err != nil {
				t.Fatal(err)
			}
			if got := firstErrCode(err); got != tc.code {
				t.Fatalf("code = %q, want %q: %v", got, tc.code, err)
			}
		})
	}
}
