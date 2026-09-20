package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// The `fip` half of #9733, end to end: a same-shape `map` over an `own`
// array is a CHECKED space contract, not an optimizer's hope. E053 admits the
// call on an `own` receiver, E068 verifies R7 wrote it through the donor
// (internal/ir/array_inplace_test.go pins the refusals), and here the
// annotated function runs on every backend and the bump cursor does not move
// across 200 whole-array maps — the enabled guarantee the annotation makes
// visible. The interpreter leg pins value-correctness only (its
// __heap_bump_bytes stub reads 0).
//
// The second half is the aliasing case #9733 asks a test to prove: a donor
// that is still held elsewhere is NOT mutated. `a` dies at the call, so E051
// admits it as an `own` argument, but `b` holds the same buffer — the
// uniqueness guard copies, `b` reads unchanged, and the space claim is a shape
// claim exactly as it is for a shared `fbip` input.
const fipOwnedMapSrc = `import "std/array";

fip function dbl(x: i64): i64 { return x * (2 as i64); }
fip function twice(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => dbl(x)); }

function build(n: i32): i64[] {
	var xs: i64[] = [];
	var i: i32 = 0;
	while (i < n) { xs = xs.append((i as i64) + (1 as i64)); i = i + 1; }
	return xs;
}

function churn(own xs: i64[], rounds: i32): i64[] {
	if (rounds == 0) { return xs; }
	return churn(twice(xs), rounds - 1);
}

function main(): i32 {
	var xs: i64[] = build(64);
	var before: i64 = __heap_bump_bytes();
	xs = churn(xs, 200);
	var grew: i64 = __heap_bump_bytes() - before;
	if (xs.len() != 64) { return 90; }
	if (xs[0] == 1 as i64) { return 91; }
	if (grew != 0 as i64) { return 92; }

	// a = twice(a) is the shape E051 admits for a local: it dies at the
	// call. b still holds the buffer, so the donor reaches the guard shared.
	var a: i64[] = build(4);
	var b: i64[] = a;
	a = twice(a);
	if (b[0] != 1 as i64 || b[3] != 4 as i64) { return 93; }
	if (a[0] != 2 as i64 || a[3] != 8 as i64) { return 94; }
	if (b.len() != 4 || a.len() != 4) { return 95; }
	return 0;
}
`

// 90-92: the unique path (wrong length, unchanged data, heap grew); 93-95:
// the shared donor (alias mutated, result wrong, lengths).
func TestX86_64FipOwnedMapZeroAllocAndSharedDonorImmutable(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, fipOwnedMapSrc); code != 0 {
		t.Errorf("fip owned map on x86-64: got %d, want 0", code)
	}
}

func TestArm64FipOwnedMapZeroAllocAndSharedDonorImmutable(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, fipOwnedMapSrc); code != 0 {
		t.Errorf("fip owned map on arm64: got %d, want 0", code)
	}
}

func TestWASMFipOwnedMapZeroAllocAndSharedDonorImmutable(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, fipOwnedMapSrc); got != 0 {
		t.Errorf("fip owned map on wasm: got %d, want 0", got)
	}
}

func TestInterpFipOwnedMapValueCorrect(t *testing.T) {
	if got := runInterpExit(t, fipOwnedMapSrc); got != 0 {
		t.Errorf("fip owned map on interp: got %d, want 0", got)
	}
}
