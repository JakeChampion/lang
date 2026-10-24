package e2ecompiler

import (
	"testing"
)

var cellWasmIRCases = []struct {
	name string
	src  string
	want int
}{
	{"get", `function main(): i32 { let c: Cell[i32] = cell_new(7); return c.get(); }`, 7},
	{"set", `function main(): i32 { let c: Cell[i32] = cell_new(1); c.set(9); return c.get(); }`, 9},
	{"read-modify-write", `function main(): i32 { let c: Cell[i32] = cell_new(0); c.set(c.get() + 5); c.set(c.get() * 2); return c.get(); }`, 10},
	{"shared-through-call", `function bump(c: Cell[i32]): void { c.set(c.get() + 1); } function main(): i32 { let c: Cell[i32] = cell_new(10); bump(c); bump(c); bump(c); return c.get(); }`, 13},
	{"struct-field", `struct Box { c: Cell[i32] } function main(): i32 { let b: Box = Box { c: cell_new(5) }; b.c.set(b.c.get() + 1); return b.c.get(); }`, 6},
	{"local-f64", `function main(): i32 { let c: Cell[f64] = cell_new(2.5); return (c.get() * 2.0) as i32; }`, 5},
	{"struct-field-f64", `struct Box { c: Cell[f64] } function main(): i32 { let b: Box = Box { c: cell_new(2.5) }; return (b.c.get() * 2.0) as i32; }`, 5},
	{"struct-field-f64-set", `struct Box { c: Cell[f64] } function main(): i32 { let b: Box = Box { c: cell_new(2.5) }; b.c.set(b.c.get() + 0.5); return (b.c.get() * 2.0) as i32; }`, 6},
	{"struct-field-i64", `struct Box { c: Cell[i64] } function main(): i32 { let b: Box = Box { c: cell_new(5000000000 as i64) }; return (b.c.get() / (1000000000 as i64)) as i32; }`, 5},
	{"tuple-elem-ctor", `function main(): i32 { let t: (i32, Cell[i32]) = (7, cell_new(5)); return t.1.get() + t.0; }`, 12},
	{"tuple-elem-local", `function main(): i32 { let c: Cell[i32] = cell_new(5); let t: (i32, Cell[i32]) = (7, c); return t.1.get() + t.0; }`, 12},
	{"tuple-elem-set", `function main(): i32 { let t: (i32, Cell[i32]) = (7, cell_new(0)); t.1.set(t.1.get() + t.0); return t.1.get(); }`, 7},
	{"tuple-elem-f64", `function main(): i32 { let t: (i32, Cell[f64]) = (1, cell_new(2.5)); return (t.1.get() * 2.0) as i32; }`, 5},
	{"tuple-elem-shared", `function main(): i32 { let c: Cell[i32] = cell_new(1); let a: (i32, Cell[i32]) = (1, c); let b: (i32, Cell[i32]) = (2, c); a.1.set(9); return b.1.get(); }`, 9},
	{"enum-payload", `enum H { Has(Cell[i32]), No } function main(): i32 { let h: H = Has(cell_new(4)); match (h) { Has(c) => { c.set(c.get() + 3); return c.get(); }, No => { return 0; } } }`, 7},
	{"enum-payload-f64", `enum H { Has(Cell[f64]), No } function main(): i32 { let h: H = Has(cell_new(2.5)); match (h) { Has(c) => { return (c.get() * 2.0) as i32; }, No => { return 0; } } }`, 5},
	{"tuple-elem-f32", `function main(): i32 { let t: (i32, Cell[f32]) = (1, cell_new(2.5 as f32)); return (t.1.get() * 2.0) as i32; }`, 5},
	{"enum-payload-f32", `enum H { Has(Cell[f32]), No } function main(): i32 { let h: H = Has(cell_new(2.5 as f32)); match (h) { Has(c) => { return (c.get() * 2.0) as i32; }, No => { return 0; } } }`, 5},
	{"unannotated-cell-f32", `function main(): i32 { let c = cell_new(2.5 as f32); return (c.get() * 2.0) as i32; }`, 5},
}

func TestSelfHostCellWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range cellWasmIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			if code := runInterpExit(t, tc.src+"\n"); code != tc.want {
				t.Fatalf("interpreter exited %d, want %d", code, tc.want)
			}
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
