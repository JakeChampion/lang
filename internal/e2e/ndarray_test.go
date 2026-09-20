package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// std/ndarray's materialization rule (docs/ARRAY-SHAPES.md, #9734), held to
// by the allocation counters rather than by inspection: the structural
// operations — transpose, permute, reverse, slice, select, and reshape of a
// row-major handle — move no elements, and the ones that must copy —
// reshape of a strided handle, packed(), to_flat() of anything not packed —
// copy exactly then. The element buffer is 32 KiB; a metadata operation is
// bounded well under 1 KiB (a handle and two small index arrays) and a copy
// is at least the buffer, so the two are not confusable. Every result is
// read again at the very end: a local is released after its LAST use, not
// at scope exit, and a released buffer goes back to the freelist where the
// next copy of the same size takes it for free and the bump counter never
// moves — which is exactly what the first draft of this test measured.
//
// The shape is a runtime value throughout — nothing states a dimension
// statically — and the values are checked against the flat construction
// on every backend. The interpreter's counter stub reads 0, so the program
// detects that and checks values only there.
const ndarraySrc = `import "std/ndarray";

function build(n: i32): i64[] {
	var xs: i64[] = [];
	var i: i32 = 0;
	while (i < n) { xs = xs.append(i as i64); i = i + 1; }
	return xs;
}

function main(): i32 {
	var n: i32 = 64;
	var probe0: i64 = __heap_bump_bytes();
	var xs: i64[] = build(n * n);
	var counters: boolean = __heap_bump_bytes() - probe0 > 0 as i64;
	var elem_bytes: i64 = (n * n * 8) as i64;
	var small: i64 = 1024 as i64;

	var a: ndarray.NdArray[i64] = ndarray.from_flat(xs, [n, n]);
	if (a.rank() != 2 || a.len() != n * n || a.shape()[1] != n) { return 10; }
	if (a.get([3, 5]) != (3 * n + 5) as i64) { return 11; }
	if (!a.is_row_major() || !a.is_packed()) { return 12; }

	var b0: i64 = __heap_bump_bytes();
	var t: ndarray.NdArray[i64] = a.transpose();
	var d_t: i64 = __heap_bump_bytes() - b0;
	if (t.get([3, 5]) != a.get([5, 3]) || t.shape()[0] != n) { return 20; }
	if (counters && d_t >= small) { return 21; }
	if (t.is_row_major()) { return 22; }

	var b1: i64 = __heap_bump_bytes();
	var rv: ndarray.NdArray[i64] = a.reverse(1);
	var sl: ndarray.NdArray[i64] = a.slice(0, 10, 20);
	var se: ndarray.NdArray[i64] = a.select(1, 9);
	var pm: ndarray.NdArray[i64] = a.permute([1, 0]);
	var d_meta: i64 = __heap_bump_bytes() - b1;
	if (rv.get([2, 0]) != a.get([2, n - 1])) { return 30; }
	if (sl.shape()[0] != 10 || sl.get([0, 7]) != a.get([10, 7])) { return 31; }
	if (se.rank() != 1 || se.get([4]) != a.get([4, 9])) { return 32; }
	if (pm.get([3, 5]) != a.get([5, 3])) { return 33; }
	if (counters && d_meta >= small) { return 34; }

	var col: ndarray.NdArray[i64] = t.select(0, 9);
	if (col.get([4]) != a.get([4, 9])) { return 40; }
	if (rv.reverse(1).get([2, 0]) != a.get([2, 0])) { return 41; }
	if (sl.slice(0, 2, 5).get([0, 1]) != a.get([12, 1])) { return 42; }

	var b2: i64 = __heap_bump_bytes();
	var r2: ndarray.NdArray[i64] = a.reshape([n * n]);
	var d_r2: i64 = __heap_bump_bytes() - b2;
	if (r2.get([n + 1]) != a.get([1, 1])) { return 50; }
	if (counters && d_r2 >= small) { return 51; }

	var b3: i64 = __heap_bump_bytes();
	var r3: ndarray.NdArray[i64] = t.reshape([n * n]);
	var d_r3: i64 = __heap_bump_bytes() - b3;
	if (r3.get([1]) != a.get([1, 0])) { return 60; }
	if (counters && d_r3 < elem_bytes) { return 61; }

	var b4: i64 = __heap_bump_bytes();
	var f1: i64[] = a.to_flat();
	var d_f1: i64 = __heap_bump_bytes() - b4;
	if (f1.len() != n * n || f1[n + 1] != a.get([1, 1])) { return 70; }
	if (counters && d_f1 >= small) { return 71; }

	var b5: i64 = __heap_bump_bytes();
	var f2: i64[] = t.to_flat();
	var d_f2: i64 = __heap_bump_bytes() - b5;
	if (f2.len() != n * n || f2[1] != a.get([1, 0])) { return 80; }
	if (counters && d_f2 < elem_bytes) { return 81; }

	var b6: i64 = __heap_bump_bytes();
	var p: ndarray.NdArray[i64] = t.packed();
	var d_p: i64 = __heap_bump_bytes() - b6;
	if (!p.is_packed() || p.get([3, 5]) != t.get([3, 5])) { return 90; }
	if (counters && d_p < elem_bytes) { return 91; }
	if (!a.packed().is_packed()) { return 92; }

	// A reversed handle is the one strided shape a transpose does not
	// cover: negative strides and an offset at the far end, walked by the
	// same odometer.
	var b7: i64 = __heap_bump_bytes();
	var rvf: i64[] = rv.to_flat();
	var d_rvf: i64 = __heap_bump_bytes() - b7;
	if (rvf.len() != n * n || rvf[0] != a.get([0, n - 1]) || rvf[n - 1] != a.get([0, 0])) { return 93; }
	if (rvf[n] != a.get([1, n - 1]) || rvf[n * n - 1] != a.get([n - 1, 0])) { return 94; }
	if (counters && d_rvf < elem_bytes) { return 95; }
	var both: ndarray.NdArray[i64] = a.reverse(0).reverse(1).packed();
	if (!both.is_packed() || both.get([0, 0]) != a.get([n - 1, n - 1]) || both.get([n - 1, n - 1]) != a.get([0, 0])) { return 96; }

	var z: ndarray.NdArray[i64] = ndarray.from_flat([7 as i64], []);
	if (z.rank() != 0 || z.len() != 1 || z.get([]) != 7 as i64) { return 100; }
	var e: ndarray.NdArray[i64] = a.slice(0, 5, 5);
	if (e.len() != 0 || e.to_flat().len() != 0) { return 101; }

	// Elementwise: one packed buffer of the input's size, over a strided
	// input as much as a packed one.
	var b8: i64 = __heap_bump_bytes();
	var m: ndarray.NdArray[i64] = t.map((x: i64): i64 => x * (2 as i64));
	var d_m: i64 = __heap_bump_bytes() - b8;
	if (!m.is_packed() || m.shape()[0] != n || m.get([3, 5]) != (2 * (5 * n + 3)) as i64) { return 120; }
	if (counters && d_m < elem_bytes) { return 121; }
	var zw: ndarray.NdArray[i64] = a.zip_with(t, (x: i64, y: i64): i64 => x - y);
	if (zw.get([3, 5]) != a.get([3, 5]) - a.get([5, 3]) || zw.get([7, 7]) != 0 as i64) { return 122; }

	// Along an axis: the result is lane-sized, never buffer-sized, and
	// every lane folds in increasing index order — the order-sensitive
	// fold is what a float reduction relies on (docs/ARRAY-ALGEBRA.md §3).
	var b9: i64 = __heap_bump_bytes();
	var rows: ndarray.NdArray[i64] = a.reduce_axis(1, 0 as i64, (acc: i64, x: i64): i64 => acc + x);
	var d_rows: i64 = __heap_bump_bytes() - b9;
	if (rows.rank() != 1 || rows.shape()[0] != n) { return 130; }
	if (rows.get([3]) != (3 * n * n + n * (n - 1) / 2) as i64) { return 131; }
	if (counters && d_rows >= elem_bytes) { return 132; }
	var ord: ndarray.NdArray[i64] = a.slice(0, 0, 2).slice(1, 0, 3).reduce_axis(1, 0 as i64, (acc: i64, x: i64): i64 => acc * (1000 as i64) + x);
	if (ord.get([0]) != 1002 as i64 || ord.get([1]) != (64065066 as i64)) { return 133; }
	var sc0: ndarray.NdArray[i64] = t.scan_axis(0, 0 as i64, (acc: i64, x: i64): i64 => acc + x);
	if (sc0.shape()[0] != n || sc0.get([3, 5]) != (5 * n * 4 + 6) as i64 || sc0.get([0, 5]) != a.get([5, 0])) { return 134; }
	// A reversed handle is where index order and storage order disagree,
	// so a kernel that walked storage would fold these backwards.
	var ordrv: ndarray.NdArray[i64] = rv.slice(0, 0, 1).slice(1, 0, 3).reduce_axis(1, 0 as i64, (acc: i64, x: i64): i64 => acc * (1000 as i64) + x);
	if (ordrv.get([0]) != (63062061 as i64)) { return 136; }
	var scrv: ndarray.NdArray[i64] = rv.slice(1, 0, 3).scan_axis(1, 0 as i64, (acc: i64, x: i64): i64 => acc * (1000 as i64) + x);
	if (scrv.get([1, 0]) != 127 as i64 || scrv.get([1, 2]) != (127126125 as i64)) { return 137; }
	var one: ndarray.NdArray[i64] = rows.reduce_axis(0, 0 as i64, (acc: i64, x: i64): i64 => acc + x);
	var total: i64 = a.fold_all(0 as i64, (acc: i64, x: i64): i64 => acc + x);
	if (one.rank() != 0 || one.get([]) != total || total != ((n * n) as i64) * ((n * n - 1) as i64) / (2 as i64)) { return 135; }

	// Broadcasting: a stretched axis is stride 0, so broadcast_to is
	// metadata, and zip_with over a row, a column and a scalar allocates
	// the one result buffer.
	var b10: i64 = __heap_bump_bytes();
	var rowv: ndarray.NdArray[i64] = a.select(0, 0);
	var wide: ndarray.NdArray[i64] = rowv.broadcast_to([n, n]);
	var d_bc: i64 = __heap_bump_bytes() - b10;
	if (wide.rank() != 2 || wide.strides()[0] != 0 || wide.get([5, 3]) != a.get([0, 3])) { return 140; }
	if (counters && d_bc >= small) { return 141; }
	if (wide.is_row_major() || wide.to_flat()[n + 3] != a.get([0, 3])) { return 142; }
	var sub: ndarray.NdArray[i64] = a.zip_with(rowv, (x: i64, y: i64): i64 => x - y);
	if (sub.shape()[0] != n || sub.get([5, 3]) != (5 * n) as i64) { return 143; }
	var colv: ndarray.NdArray[i64] = a.slice(1, 0, 1);
	var diff: ndarray.NdArray[i64] = a.zip_with(colv, (x: i64, y: i64): i64 => x - y);
	if (diff.shape()[1] != n || diff.get([5, 3]) != 3 as i64) { return 144; }
	var scal: ndarray.NdArray[i64] = ndarray.from_flat([2 as i64], []);
	var dbl: ndarray.NdArray[i64] = scal.zip_with(a, (x: i64, y: i64): i64 => x * y);
	if (dbl.rank() != 2 || dbl.get([5, 3]) != (2 * (5 * n + 3)) as i64) { return 145; }
	var op: ndarray.NdArray[i64] = colv.zip_with(rowv, (x: i64, y: i64): i64 => x * y);
	if (op.shape()[0] != n || op.shape()[1] != n || op.get([5, 3]) != (5 * n * 3) as i64) { return 146; }
	var bs: i32[] = ndarray.broadcast_shape([1, n], [n, 1]);
	if (bs.len() != 2 || bs[0] != n || bs[1] != n) { return 147; }

	// The keep-alive: every buffer measured above is still held here.
	var live: i32 = t.rank() + rv.rank() + sl.rank() + se.rank() + pm.rank() + col.rank()
		+ r2.rank() + r3.rank() + f1.len() + f2.len() + p.rank() + z.rank() + e.rank()
		+ rvf.len() + both.rank() + m.rank() + zw.rank() + rows.rank() + ord.rank()
		+ sc0.rank() + ordrv.rank() + scrv.rank() + one.rank() + rowv.rank() + wide.rank()
		+ sub.rank() + colv.rank() + diff.rank() + scal.rank() + dbl.rank() + op.rank();
	if (live != 3 * n * n + 42) { return 110; }
	return 0;
}
`

// A shape that does not account for its storage, two shapes that do not
// broadcast, a broadcast that would drop an axis, a negative extent, a
// count that does not fit an i32, and an axis that is not one of the
// handle's are each a derived-shape error, and
// docs/ARRAY-ALGEBRA.md §4 makes that an abort rather than a truncation.
var ndarrayAbortSrcs = map[string]string{
	"a shape that does not fit its storage": `import "std/ndarray";
function main(): i32 {
	var a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4, 5], [2, 3]);
	return a.rank();
}
`,
	"zip_with over two shapes of one rank that do not broadcast": `import "std/ndarray";
function main(): i32 {
	var a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4, 5, 6], [2, 3]);
	var b: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4, 5, 6], [3, 2]);
	return a.zip_with(b, (x: i32, y: i32): i32 => x + y).rank();
}
`,
	"zip_with over a row of the wrong extent": `import "std/ndarray";
function main(): i32 {
	var a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4, 5, 6], [2, 3]);
	var b: ndarray.NdArray[i32] = ndarray.from_flat([1, 2], [2]);
	return a.zip_with(b, (x: i32, y: i32): i32 => x + y).rank();
}
`,
	"broadcast_to a shape of lower rank": `import "std/ndarray";
function main(): i32 {
	var a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4, 5, 6], [2, 3]);
	return a.broadcast_to([6]).rank();
}
`,
	"broadcast_to a shape with a negative extent": `import "std/ndarray";
function main(): i32 {
	var a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3], [3]);
	return a.broadcast_to([-1, 3]).rank();
}
`,
	"broadcast_shape with a negative extent": `import "std/ndarray";
function main(): i32 {
	return ndarray.broadcast_shape([-2, 3], [1, 3]).len();
}
`,
	"broadcast_to a shape whose count wraps negative": `import "std/ndarray";
function main(): i32 {
	var a: ndarray.NdArray[i32] = ndarray.from_flat([1], [1, 1]);
	return a.broadcast_to([50000, 50000]).rank();
}
`,
	"broadcast_to a shape whose count wraps to zero": `import "std/ndarray";
function main(): i32 {
	var a: ndarray.NdArray[i32] = ndarray.from_flat([1], [1, 1]);
	return a.broadcast_to([65536, 65536]).rank();
}
`,
	"broadcast_shape whose result does not fit an i32": `import "std/ndarray";
function main(): i32 {
	return ndarray.broadcast_shape([50000, 1], [1, 50000]).len();
}
`,
	"reduce_axis over an axis the handle lacks": `import "std/ndarray";
function main(): i32 {
	var a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4, 5, 6], [2, 3]);
	return a.reduce_axis(2, 0, (acc: i32, x: i32): i32 => acc + x).rank();
}
`,
}

// 1x = construction, 2x = transpose, 3x = the other metadata operations,
// 4x = chains, 5x/6x = reshape (metadata / copy), 7x/8x = to_flat (no copy /
// copy), 90-92 = packed, 93-96 = a reversed handle materialized, 10x = rank
// 0 and empty, 12x = elementwise, 13x = along an axis, 14x = broadcasting.
func TestX86_64NdarrayStructuralOpsAreMetadata(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ndarraySrc); code != 0 {
		t.Errorf("ndarray on x86-64: got %d, want 0", code)
	}
	for name, src := range ndarrayAbortSrcs {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 134 {
			t.Errorf("%s on x86-64: exit %d, want the bounds abort 134", name, code)
		}
	}
}

func TestArm64NdarrayStructuralOpsAreMetadata(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ndarraySrc); code != 0 {
		t.Errorf("ndarray on arm64: got %d, want 0", code)
	}
	for name, src := range ndarrayAbortSrcs {
		if _, code := compileAndRunArm64FreeOn(t, src); code != 134 {
			t.Errorf("%s on arm64: exit %d, want the bounds abort 134", name, code)
		}
	}
}

func TestWASMNdarrayStructuralOpsAreMetadata(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, ndarraySrc); got != 0 {
		t.Errorf("ndarray on wasm: got %d, want 0", got)
	}
	// `exit(134)` collapses to wasmtime's exit 1, and runWasm fails the test
	// on any non-zero exit, so the abort is asserted the way the array
	// bounds trap is.
	for name, src := range ndarrayAbortSrcs {
		if _, _, trapped := runWasmExpectingTrap(t, src); !trapped {
			t.Errorf("%s on wasm: exit 0, want a failure", name)
		}
	}
}

func TestInterpNdarrayValuesCorrect(t *testing.T) {
	if got := runInterpExit(t, ndarraySrc); got != 0 {
		t.Errorf("ndarray on interp: got %d, want 0", got)
	}
	for name, src := range ndarrayAbortSrcs {
		if got := runInterpExit(t, src); got == 0 {
			t.Errorf("%s on interp: exit 0, want a failure", name)
		}
	}
}
