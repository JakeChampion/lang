package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestSHAHWBuiltinSignatures(t *testing.T) {
	type sigCase struct {
		name string
		src  string
		want string
	}
	var cases []sigCase
	for _, alg := range []string{"sha256", "sha1"} {
		r := strings.NewReplacer("PRED", "__"+alg+"_hw", "KERNEL", "__"+alg+"_hw_blocks")
		for _, c := range []sigCase{
			{"the predicate is a boolean", `function main(): i32 { if (PRED()) { return 1; } return 0; }`, ""},
			{"the kernel takes views", `function main(): i32 { let s: u8[] = __alloc_u8(32); let out: u8[] = KERNEL(s, s[0:16]); let more: u8[] = KERNEL(out, "abc".as_bytes()); return more.len(); }`, ""},
			{"the kernel refuses an i32[]", `function main(): i32 { let s: i32[] = [1, 2]; let out: u8[] = KERNEL(s, s); return 0; }`, "argument"},
			{"the kernel takes two arguments", `function main(): i32 { let s: u8[] = __alloc_u8(32); let out: u8[] = KERNEL(s); return 0; }`, "argument"},
			{"the predicate takes nothing", `function main(): i32 { if (PRED(1)) { return 1; } return 0; }`, "argument"},
			{"the predicate is not an i32", `function main(): i32 { let r: i32 = PRED(); return r; }`, "boolean"},
		} {
			cases = append(cases, sigCase{alg + ": " + c.name, r.Replace(c.src), c.want})
		}
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
