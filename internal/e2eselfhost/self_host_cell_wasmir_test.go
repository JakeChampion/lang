package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Cell primitives on the self-host WASM **IR** driver (#5510). The existing
// Cell coverage (`TestSelfHostWasmRun`'s cell-* cases) drives wasm_run.fern,
// the AST driver; this pins the wasm_ir_run.fern path, which is the one #5510
// is about.
//
// irlower lowers a Cell as a one-element array — `cell_new(v)` → `[v]`,
// `c.get()` → `c[0]`, `c.set(x)` → `c[0] = x` — so it rides the array
// machinery every backend already has rather than needing three dedicated
// wasm ops. These cases exist to pin that, since the desugar is the only thing
// standing between the wasm IR path and a `$cell_new` link failure.
var cellWasmIRCases = []struct {
	name string
	src  string
	want int
}{
	{"get", `function main(): i32 { var c: Cell[i32] = cell_new(7); return c.get(); }`, 7},
	{"set", `function main(): i32 { var c: Cell[i32] = cell_new(1); c.set(9); return c.get(); }`, 9},
	{"read-modify-write", `function main(): i32 { var c: Cell[i32] = cell_new(0); c.set(c.get() + 5); c.set(c.get() * 2); return c.get(); }`, 10},
	// A Cell passed to a function is shared, not copied — the mutation is
	// visible to the caller. This is the whole point of the type.
	{"shared-through-call", `function bump(c: Cell[i32]): void { c.set(c.get() + 1); } function main(): i32 { var c: Cell[i32] = cell_new(10); bump(c); bump(c); bump(c); return c.get(); }`, 13},
	// A Cell living in a struct field: the receiver is a field access rather
	// than a bare ident.
	{"struct-field", `struct Box { c: Cell[i32] } function main(): i32 { var b: Box = Box { c: cell_new(5) }; b.c.set(b.c.get() + 1); return b.c.get(); }`, 6},
	// A WIDE element through that field access. The i32 case above passes on a
	// 4-byte read either way, so it could not catch the width being taken from
	// the field's declared spelling: a LOCAL `Cell[f64]` records its element
	// kind on the slot, while a FIELD reads `Cell[f64]`, which none of the
	// array classifiers matched — so `b.c.get()` indexed 4-byte and the module
	// failed to validate outright ("expected f64, found i32"). That is what
	// made a self-host-built interp_run.wasm invalid: interp.fern's Value has
	// `VCellF { c: Cell[f64] }`, read at interp.fern:657.
	//
	// The local twin is here as the control that already worked, so a
	// regression tells you which of the two paths broke.
	{"local-f64", `function main(): i32 { var c: Cell[f64] = cell_new(2.5); return (c.get() * 2.0) as i32; }`, 5},
	{"struct-field-f64", `struct Box { c: Cell[f64] } function main(): i32 { var b: Box = Box { c: cell_new(2.5) }; return (b.c.get() * 2.0) as i32; }`, 5},
	// set as well as get: the two took their width from different places —
	// set consulted the cell's element type, get went through the array
	// classifiers — so only one of them was wrong.
	{"struct-field-f64-set", `struct Box { c: Cell[f64] } function main(): i32 { var b: Box = Box { c: cell_new(2.5) }; b.c.set(b.c.get() + 0.5); return (b.c.get() * 2.0) as i32; }`, 6},
	// The other 8-byte element, whose classifiers carry the same field arm.
	{"struct-field-i64", `struct Box { c: Cell[i64] } function main(): i32 { var b: Box = Box { c: cell_new(5000000000 as i64) }; return (b.c.get() / (1000000000 as i64)) as i32; }`, 5},
	// A cell as a TUPLE ELEMENT. Two separate gaps met here, which is why both
	// spellings of the construction are pinned: a cell LOCAL element was already
	// admitted (its slot is marked is_arr), while a direct `cell_new(v)` element
	// bailed the whole module at `tuple_elem_ctor_eligible` — the difference
	// being nothing but whether the cell was named first. And the READ needed
	// the element to be tagged by its declared `Cell[T]` spelling rather than by
	// the element's own width, or `t.1.get()` dispatched as a method on the
	// element type and bailed as the unknown symbol `i32.get`.
	{"tuple-elem-ctor", `function main(): i32 { var t: (i32, Cell[i32]) = (7, cell_new(5)); return t.1.get() + t.0; }`, 12},
	{"tuple-elem-local", `function main(): i32 { var c: Cell[i32] = cell_new(5); var t: (i32, Cell[i32]) = (7, c); return t.1.get() + t.0; }`, 12},
	{"tuple-elem-set", `function main(): i32 { var t: (i32, Cell[i32]) = (7, cell_new(0)); t.1.set(t.1.get() + t.0); return t.1.get(); }`, 7},
	// The wide element through a tuple element, the twin of struct-field-f64:
	// an i32 element reads 4-byte either way, so only a wide one can show the
	// width being taken from the wrong place.
	{"tuple-elem-f64", `function main(): i32 { var t: (i32, Cell[f64]) = (1, cell_new(2.5)); return (t.1.get() * 2.0) as i32; }`, 5},
	// One cell shared by two tuples: the mutation through one is visible
	// through the other, which is what says the element is the cell itself and
	// not a copy.
	{"tuple-elem-shared", `function main(): i32 { var c: Cell[i32] = cell_new(1); var a: (i32, Cell[i32]) = (1, c); var b: (i32, Cell[i32]) = (2, c); a.1.set(9); return b.1.get(); }`, 9},
	// A cell as an ENUM VARIANT PAYLOAD, reached through a match binding. The
	// bound name needed the same is_cell marking a `var` annotation gives it;
	// without it `c.get()` bailed the module exactly as the tuple element did.
	{"enum-payload", `enum H { Has(Cell[i32]), No } function main(): i32 { var h: H = Has(cell_new(4)); match (h) { Has(c) => { c.set(c.get() + 3); return c.get(); }, No => { return 0; } } }`, 7},
	{"enum-payload-f64", `enum H { Has(Cell[f64]), No } function main(): i32 { var h: H = Has(cell_new(2.5)); match (h) { Has(c) => { return (c.get() * 2.0) as i32; }, No => { return 0; } } }`, 5},
}

// TestSelfHostCellWasmIR runs each case through the self-hosted wasm IR driver
// and checks it against the native backend as the oracle.
func TestSelfHostCellWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host Cell wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range cellWasmIRCases {
		t.Run(tc.name, func(t *testing.T) {
			// Native cross-check first: if the oracle disagrees the case is
			// wrong, not the backend.
			if _, code := compileAndRunX86_64(t, tc.src+"\n"); code != tc.want {
				t.Fatalf("native x86-64 exited %d, want %d", code, tc.want)
			}
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("wasm IR driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, "cell_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("Cell wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
