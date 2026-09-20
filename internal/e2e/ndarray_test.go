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

	var z: ndarray.NdArray[i64] = ndarray.from_flat([7 as i64], []);
	if (z.rank() != 0 || z.len() != 1 || z.get([]) != 7 as i64) { return 100; }
	var e: ndarray.NdArray[i64] = a.slice(0, 5, 5);
	if (e.len() != 0 || e.to_flat().len() != 0) { return 101; }

	// The keep-alive: every buffer measured above is still held here.
	var live: i32 = t.rank() + rv.rank() + sl.rank() + se.rank() + pm.rank() + col.rank()
		+ r2.rank() + r3.rank() + f1.len() + f2.len() + p.rank() + z.rank() + e.rank();
	if (live != 2 * n * n + 16) { return 110; }
	return 0;
}
`

// A shape that does not account for its storage is a derived-shape error,
// and docs/ARRAY-ALGEBRA.md §4 makes that an abort rather than a truncation.
const ndarrayShapeErrorSrc = `import "std/ndarray";
function main(): i32 {
	var a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4, 5], [2, 3]);
	return a.rank();
}
`

// 1x = construction, 2x = transpose, 3x = the other metadata operations,
// 4x = chains, 5x/6x = reshape (metadata / copy), 7x/8x = to_flat (no copy /
// copy), 9x = packed, 10x = rank 0 and empty.
func TestX86_64NdarrayStructuralOpsAreMetadata(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ndarraySrc); code != 0 {
		t.Errorf("ndarray on x86-64: got %d, want 0", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, ndarrayShapeErrorSrc); code != 134 {
		t.Errorf("a shape that does not fit its storage on x86-64: exit %d, want the bounds abort 134", code)
	}
}

func TestArm64NdarrayStructuralOpsAreMetadata(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ndarraySrc); code != 0 {
		t.Errorf("ndarray on arm64: got %d, want 0", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, ndarrayShapeErrorSrc); code != 134 {
		t.Errorf("a shape that does not fit its storage on arm64: exit %d, want the bounds abort 134", code)
	}
}

func TestWASMNdarrayStructuralOpsAreMetadata(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, ndarraySrc); got != 0 {
		t.Errorf("ndarray on wasm: got %d, want 0", got)
	}
	if got := runWasm(t, ndarrayShapeErrorSrc); got == 0 {
		t.Errorf("a shape that does not fit its storage on wasm: exit 0, want a failure")
	}
}

func TestInterpNdarrayValuesCorrect(t *testing.T) {
	if got := runInterpExit(t, ndarraySrc); got != 0 {
		t.Errorf("ndarray on interp: got %d, want 0", got)
	}
	if got := runInterpExit(t, ndarrayShapeErrorSrc); got == 0 {
		t.Errorf("a shape that does not fit its storage on interp: exit 0, want a failure")
	}
}
