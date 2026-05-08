package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// FuzzCheck exercises the type checker against random programs that
// happen to parse. Property: the checker must never panic, regardless
// of the AST shape it's handed. Inputs that fail to parse are skipped
// silently because they say nothing about the checker.
//
// Run with: go test -fuzz=FuzzCheck ./internal/checker
func FuzzCheck(f *testing.F) {
	seeds := []string{
		`function f(): i32 { return 1; }`,
		`function f(n: i32): i32 { return n + 1; }`,
		`function f(): boolean { return true && (1 < 2); }`,
		`function f(): i32 { var a: i32[] = [1, 2, 3]; return a[1]; }`,
		`function f(): i32 {
			var sum = 0;
			var i = 0;
			while (i < 10) { sum = sum + i; i = i + 1; }
			return sum;
		}`,
		// Programs that already type-check incorrectly. The checker
		// should still come back without panicking — it just collects
		// errors.
		`function f(): i32 { return true; }`,
		`function f(): i32 { return undefined_thing; }`,
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
