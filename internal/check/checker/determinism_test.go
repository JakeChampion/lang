package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func errorString(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, cerr := Check(prog)
	if cerr == nil {
		t.Fatalf("expected a type error, program checked clean:\n%s", src)
	}
	return cerr.Error()
}

var errorMatrix = map[string]string{
	"two_errors_one_func": `
function f(): i32 {
	return true;
	let x: i32 = unknownThing;
}`,

	"errors_across_funcs": `
function a(): i32 { return true; }
function b(): boolean { return 1; }
function c(): i32 { return missing; }`,

	"bad_struct_fields": `
struct S { a: i32, b: i32, c: i32 }
function main(): i32 {
	let s: S = S { a: true, b: missing, c: false };
	return 0;
}`,

	"mixed_error_kinds": `
function f(n: i32): i32 {
	if (n) { return 0; }
	let x: i32 = true;
	return undefinedThing;
}`,

	"multiple_undefineds": `
function main(): i32 {
	let a: i32 = one;
	let b: i32 = two;
	let c: i32 = three;
	return a + b + c;
}`,
}

func TestDiagnosticOrderDeterministic(t *testing.T) {
	for name, src := range errorMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			want := errorString(t, src)
			for i := 0; i < 12; i++ {
				got := errorString(t, src)
				if got != want {
					t.Fatalf("diagnostic output not deterministic on run %d:\n--- first run ---\n%s\n--- run %d ---\n%s",
						i+2, want, i+2, got)
				}
			}
		})
	}
}
