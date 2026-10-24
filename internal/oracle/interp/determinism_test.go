package interp

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func runCapture(t *testing.T, src string) (val string, stdout string) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	i := New()
	var buf bytes.Buffer
	i.Stdout = &buf
	for _, ed := range prog.Enums {
		i.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		i.Register(fn)
	}
	v, err := i.CallByName("main", nil)
	if err != nil {
		t.Fatalf("call main: %v\nsrc:\n%s", err, src)
	}
	return fmt.Sprintf("%v", v), buf.String()
}

var determinismMatrix = map[string]string{
	"arithmetic": `
function main(): i32 {
	let x: i32 = 6;
	let y: i32 = 7;
	return x * y + (y - x) / 2;
}`,

	"struct_fields": `
struct Point { x: i32, y: i32 }
function (p: Point) sum(): i32 { return p.x + p.y; }
function main(): i32 {
	let p: Point = Point { x: 3, y: 4 };
	return p.sum();
}`,

	"map_iteration": `
function main(): i32 {
	let m: Map[string, i32] = Map { "a": 1, "b": 2, "c": 3 };
	let total: i32 = 0;
	for (k, v) in m { total = total + v; }
	return total;
}`,

	"map_print_order": `
function main(): void {
	let m: Map[string, i32] = Map { "a": 1, "b": 2, "c": 3 };
	for (k, v) in m { print(k); }
}`,

	"closures": `
function adder(n: i32): (i32) => i32 {
	return (x: i32): i32 => { return x + n; };
}
function main(): i32 {
	let f: (i32) => i32 = adder(10);
	let g: (i32) => i32 = adder(20);
	return f(1) + g(2);
}`,

	"string_build": `
function main(): void {
	let xs: string[] = ["a", "b", "c", "d"];
	let out: string = "";
	for x in xs { out = out + x; }
	print(out);
}`,

	"recursion": `
function fact(n: i32): i32 {
	if (n == 0) { return 1; }
	return n * fact(n - 1);
}
function main(): i32 { return fact(6); }`,
}

func TestInterpDeterministic(t *testing.T) {
	for name, src := range determinismMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			wantVal, wantOut := runCapture(t, src)
			for i := 0; i < 8; i++ {
				gotVal, gotOut := runCapture(t, src)
				if gotVal != wantVal {
					t.Fatalf("result not deterministic on run %d: got %q, first run %q", i+2, gotVal, wantVal)
				}
				if gotOut != wantOut {
					t.Fatalf("stdout not deterministic on run %d: got %q, first run %q", i+2, gotOut, wantOut)
				}
			}
		})
	}
}
