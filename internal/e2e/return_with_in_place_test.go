package e2e

import (
	"strings"
	"testing"
)

// `return xs.with(..)` on an `own` array writes in place even when another
// `.with` on the same receiver sits in a sibling branch or in the enclosing
// loop (#9844). That other site is textually later but never on the returning
// path, and nothing reads the receiver after a return, so it must not count as
// a later use and force the copy. Before the fix bump_ret and one_ret each
// copied the buffer once per call; bump_assign, the self-reassign spelling,
// was already free and stays here as the control.
const returnWithInPlaceSrc = `function bump_ret(own idx: i32[], shape: i32[]): i32[] {
	var k: i32 = shape.len() - 1;
	while (k >= 0) {
		if (idx[k] + 1 < shape[k]) { return idx.with(k, idx[k] + 1); }
		idx = idx.with(k, 0);
		k = k - 1;
	}
	return idx;
}

function bump_assign(own idx: i32[], shape: i32[]): i32[] {
	var k: i32 = shape.len() - 1;
	while (k >= 0) {
		if (idx[k] + 1 < shape[k]) {
			idx = idx.with(k, idx[k] + 1);
			return idx;
		}
		idx = idx.with(k, 0);
		k = k - 1;
	}
	return idx;
}

function one_ret(own idx: i32[], shape: i32[]): i32[] {
	if (idx[1] + 1 < shape[1]) { return idx.with(1, idx[1] + 1); }
	return idx.with(1, 0);
}

function main(): i32 {
	var shape: i32[] = [4, 4, 4];

	var a: i32[] = [0, 0, 0];
	var at: i64 = __heap_alloc_count();
	var i: i32 = 0;
	while (i < 63) { a = bump_ret(a, shape); i = i + 1; }
	if (__heap_alloc_count() - at != (0 as i64)) { return 90; }
	if (a[0] != 3 || a[1] != 3 || a[2] != 3) { return 91; }

	var b: i32[] = [0, 0, 0];
	var at2: i64 = __heap_alloc_count();
	var j: i32 = 0;
	while (j < 63) { b = bump_assign(b, shape); j = j + 1; }
	if (__heap_alloc_count() - at2 != (0 as i64)) { return 92; }
	if (b[0] != 3 || b[1] != 3 || b[2] != 3) { return 93; }

	var c: i32[] = [0, 0, 0];
	var at3: i64 = __heap_alloc_count();
	var k: i32 = 0;
	while (k < 7) { c = one_ret(c, shape); k = k + 1; }
	if (__heap_alloc_count() - at3 != (0 as i64)) { return 94; }
	if (c[1] != 3) { return 95; }

	return 42;
}
`

// 90/92/94 name the spelling that still copies; 91/93/95 mean a result is
// wrong.
func TestReturnWithWritesInPlace(t *testing.T) {
	check := func(t *testing.T, code int, out string) {
		t.Helper()
		if code != 42 {
			t.Errorf("exit %d, want 42 — see the source for what each code names\n%s", code, strings.TrimSpace(out))
		}
	}
	t.Run("x86-64", func(t *testing.T) {
		out, code := compileAndRunX86Native(t, returnWithInPlaceSrc)
		check(t, code, out)
	})
	t.Run("arm64", func(t *testing.T) {
		out, code := compileAndRunArm64FreeOn(t, returnWithInPlaceSrc)
		check(t, code, out)
	})
	t.Run("wasm", func(t *testing.T) {
		check(t, runWasm(t, returnWithInPlaceSrc), "")
	})
}
