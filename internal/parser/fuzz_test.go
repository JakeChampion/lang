package parser

import "testing"

// FuzzParse asserts the parser never panics, regardless of input.
// Errors are fine; crashes are not. Run with `go test -fuzz=FuzzParse
// ./internal/parser` to keep generating new inputs.
func FuzzParse(f *testing.F) {
	seeds := []string{
		``,
		`function f(): number { return 1; }`,
		`function f(n: number): number { if (n == 0) { return 1; } return n; }`,
		`function f(): number { var a: number[] = [1, 2, 3]; return a[1]; }`,
		`function f(): number {
			var sum = 0;
			var i = 0;
			while (i < 10) { sum = sum + i; i = i + 1; }
			return sum;
		}`,
		`function add(a: number, b: number): number { return a + b; }
		 function main(): number { return add(40, 2); }`,
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
