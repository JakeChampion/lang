package parser

import (
	"strings"
	"testing"
)

func FuzzParse(f *testing.F) {
	seeds := []string{
		``,
		`function f(): i32 { return 1; }`,
		`function f(n: i32): i32 { if (n == 0) { return 1; } return n; }`,
		`function f(): i32 { let a: i32[] = [1, 2, 3]; return a[1]; }`,
		`function f(): i32 {
			let sum = 0;
			let i = 0;
			while (i < 10) { sum = sum + i; i = i + 1; }
			return sum;
		}`,
		`function add(a: i32, b: i32): i32 { return a + b; }
		 function main(): i32 { return add(40, 2); }`,
		"function f(): i32 { return " + strings.Repeat("(", 3000) + "1" + strings.Repeat(")", 3000) + "; }",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("parser panicked on %q: %v", src, r)
			}
		}()
		_, _ = Parse(src)
	})
}
