package parser

import "testing"

// FuzzParse asserts the parser never panics, regardless of input.
// Errors are fine; crashes are not. Run with `go test -fuzz=FuzzParse
// ./internal/parser` to keep generating new inputs.
func FuzzParse(f *testing.F) {
	seeds := []string{
		``,
		`function f(): i32 { return 1; }`,
		`function f(n: i32): i32 { if (n == 0) { return 1; } return n; }`,
		`function f(): i32 { var a: i32[] = [1, 2, 3]; return a[1]; }`,
		`function f(): i32 {
			var sum = 0;
			var i = 0;
			while (i < 10) { sum = sum + i; i = i + 1; }
			return sum;
		}`,
		`function add(a: i32, b: i32): i32 { return a + b; }
		 function main(): i32 { return add(40, 2); }`,
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
