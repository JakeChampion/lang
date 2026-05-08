package codegen

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// FuzzEmit feeds random programs through the full front end and then
// the code generator. Property: emitting must never panic, regardless
// of the AST shape it's handed. Inputs that fail to parse or fail to
// type-check are skipped silently because they say nothing about
// codegen.
//
// Run with: go test -fuzz=FuzzEmit ./internal/codegen
func FuzzEmit(f *testing.F) {
	seeds := []string{
		`function f(): i32 { return 1; }`,
		`function f(n: i32): i32 { return n * 2; }`,
		`function f(): boolean { return true && (1 < 2); }`,
		`function f(): i32 { var a: i32[] = [1, 2, 3]; return a[1]; }`,
		`function f(): i32 {
			var sum = 0;
			for (var i = 0; i < 10; i = i + 1) { sum = sum + i; }
			return sum;
		}`,
		`function main(): void { print("hi" + " there"); }`,
		`function loop(): i32 {
			while (true) { break; }
			return 0;
		}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		prog, err := parser.Parse(src)
		if err != nil || prog == nil {
			return
		}
		info, err := checker.Check(prog)
		if err != nil {
			return
		}
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("codegen panicked on %q: %v", src, r)
			}
		}()
		_, _ = Emit(prog, info)
	})
}
