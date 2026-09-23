package e2e

import "testing"

// boolMapKeyColumnSrc reads a `boolean` KEY column back through keys().
//
// The column's element stride is a property of the type, and a boolean is the
// case that reads wrong in both directions: it is not pointer-shaped — its
// runtime key kind is the scalar 0 — but its ARRAY ELEMENT strides a pointer
// width on the natives, because ast.ElemSizeBytesFor has no BoolType case and
// takes the pointer default. __map_column wrote the snapshot at a fixed 4
// until #10000.
//
// This lives in a Go test rather than the conformance corpus because the
// corpus runs on the SELF-HOST legs too, and the self-host answers a
// `Map[boolean, V]` wrong on every target — x86-64 segfaults, arm64 and wasm
// collapse `true` and `false` into one entry (#10043). A conformance case for
// this stride would pin that bug instead of this one. map_bool_column covers
// the VALUE column, which every implementation agrees on.
//
// The INSERT ORDER is the whole test. A boolean key has two inhabitants, so a
// wrong stride reads element 1 off the end of a two-element buffer — which
// lands in zero padding and reads as `false`. Insert `true` second and that
// lie is invisible; insert it second-from-the-column's-point-of-view and the
// read that should say true says false. The first version of this test had
// `true` first and passed against the un-fixed lowering.
const boolMapKeyColumnSrc = `
import "core/map";
function main(): i32 {
    var m: Map[boolean, i32] = map_new(2);
    m = m.insert(false, 7);
    m = m.insert(true, 5);
    var ks: boolean[] = m.keys();
    if (ks.len() != 2) { return 1; }
    if (ks[0]) { return 2; }
    if (!ks[1]) { return 3; }
    if (m.get_or(ks[0], 0) != 7) { return 4; }
    if (m.get_or(ks[1], 0) != 5) { return 5; }
    var t: i32 = 0;
    for k in ks { if (k) { t = t + 1; } }
    if (t != 1) { return 6; }
    return 42;
}`

func TestX86_64BoolMapKeyColumn(t *testing.T) {
	if _, code := compileAndRunX86_64(t, boolMapKeyColumnSrc); code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}

func TestArm64BoolMapKeyColumn(t *testing.T) {
	if _, code := compileAndRunArm64(t, boolMapKeyColumnSrc); code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}
