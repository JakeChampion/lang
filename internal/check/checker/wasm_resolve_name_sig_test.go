package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestWasmResolveNameSignature(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"a name and a u8[]", `function main(): i32 { let out: u8[] = __alloc_u8(17); return wasm_resolve_name("localhost", out); }`, ""},
		{"two arguments", `function main(): i32 { return wasm_resolve_name("localhost"); }`, "argument"},
		{"refuses an i32[]", `function main(): i32 { let out: i32[] = [0]; return wasm_resolve_name("localhost", out); }`, "argument"},
		{"answers an i32", `function main(): i32 { let out: u8[] = __alloc_u8(17); let s: string = wasm_resolve_name("localhost", out); return 0; }`, "string"},
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
