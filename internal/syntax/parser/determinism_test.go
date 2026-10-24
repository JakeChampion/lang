package parser_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/syntax/printer"
)

var determinismMatrix = map[string]string{
	"minimal": `function main(): i32 { return 0; }`,

	"many_funcs_strings": `
function a(): string { return "alpha"; }
function b(): string { return "beta"; }
function c(): string { return "gamma"; }
function main(): i32 {
	print(a()); print(b()); print(c());
	return 0;
}`,

	"struct_enum_mix": `
struct Point { x: i32, y: i32 }
enum Shape { Circle(i32), Rect(i32, i32) }
function area(s: Shape): i32 {
	match (s) {
		Circle(r) => { return r * r; },
		Rect(w, h) => { return w * h; }
	}
	return 0;
}
function main(): i32 {
	let p: Point = Point { x: 3, y: 4 };
	return area(Circle(p.x));
}`,

	"fstring_parts": `
function main(): i32 {
	let name: string = "world";
	let n: i32 = 42;
	print(f"hello, {name}, value={n}, end");
	return 0;
}`,

	"control_flow": `
function classify(n: i32): i32 {
	if (n > 0) { return 1; } else if (n < 0) { return -1; }
	return 0;
}
function sum_to(n: i32): i32 {
	let s: i32 = 0;
	let i: i32 = 0;
	while (i <= n) { s = s + i; i = i + 1; }
	return s;
}
function main(): i32 {
	return classify(-5) + classify(5) + classify(0) + sum_to(10);
}`,
}

func TestParseDeterministic(t *testing.T) {
	for name, src := range determinismMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			first := mustParsePrint(t, src)
			for i := 0; i < 4; i++ {
				again := mustParsePrint(t, src)
				if again != first {
					t.Fatalf("parser not deterministic on run %d: output differs (%d bytes vs %d bytes)",
						i+2, len(first), len(again))
				}
			}
		})
	}
}

func mustParsePrint(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return printer.Print(prog)
}
