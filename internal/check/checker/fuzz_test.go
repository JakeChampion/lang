package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func FuzzCheck(f *testing.F) {
	seeds := []string{
		`function f(): i32 { return 1; }`,
		`function f(n: i32): i32 { return n + 1; }`,
		`function f(): boolean { return true && (1 < 2); }`,
		`function f(): i32 { let a: i32[] = [1, 2, 3]; return a[1]; }`,
		`function f(): i32 {
			let sum = 0;
			let i = 0;
			while (i < 10) { sum = sum + i; i = i + 1; }
			return sum;
		}`,
		`function f(): i32 { return true; }`,
		`function f(): i32 { return undefined_thing; }`,
		"function f(): i32 { return " + strings.Repeat("(", 3000) + "1" + strings.Repeat(")", 3000) + "; }",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		prog, err := parser.Parse(src)
		if err != nil || prog == nil {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("checker panicked on %q: %v", src, r)
			}
		}()
		_, _ = Check(prog)
	})
}
