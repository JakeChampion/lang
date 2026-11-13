package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestPoly1305BuiltinSignature(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"a view for the message", `function main(): i32 { let m: u8[] = __alloc_u8(64); let v: [u8] = m[0:32]; let st: u8[] = __poly1305_blocks(__alloc_u8(40), v, 0, 32); return st.len(); }`, ""},
		{"a string's bytes for the message", `function main(): i32 { let st: u8[] = __poly1305_blocks(__alloc_u8(40), "sixteen bytes!!!".as_bytes(), 0, 16); return st.len(); }`, ""},
		{"the state is an owned u8[]", `function main(): i32 { let m: u8[] = __alloc_u8(40); let st: u8[] = __poly1305_blocks(m[0:40], m, 0, 16); return 0; }`, "argument"},
		{"an i32[] message is refused", `function main(): i32 { let m: i32[] = [1, 2]; let st: u8[] = __poly1305_blocks(__alloc_u8(40), m, 0, 1); return 0; }`, "argument"},
		{"four arguments", `function main(): i32 { let st: u8[] = __poly1305_blocks(__alloc_u8(40), __alloc_u8(16), 0); return 0; }`, "argument"},
		{"the result is a u8[]", `function main(): i32 { let r: i32 = __poly1305_blocks(__alloc_u8(40), __alloc_u8(16), 0, 16); return r; }`, "u8[]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = checker.Check(prog)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("check: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("check accepted %s", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("check error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
