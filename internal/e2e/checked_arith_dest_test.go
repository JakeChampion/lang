package e2e

import (
	"bytes"
	"os/exec"
	"testing"
)

// The checked operators in DESTINATION-TYPED positions: an annotated `var`, a
// `return`, and a call argument. checked_arith_test.go covers the same
// operators but reaches every one of them through `match (a +? b)`, which
// supplies no expected type — so none of it exercised the settle path, and the
// checker re-typing `a +? b` as a bare `i32` there went unnoticed.
//
// That is the whole point of this file: the operator exists to hand an
// `Option[T]` to something, and handing it to something is what was broken.
//
// Returns 0 when every case holds, else the index of the first failure.
const checkedArithDestProgram = `
function addc(a: i32, b: i32): Option[i32] { return a +? b; }
function mulc(a: i64, b: i64): Option[i64] { return a *? b; }
function subc(a: u32, b: u32): Option[u32] { return a -? b; }
function unwrap(o: Option[i32], dflt: i32): i32 {
    return match (o) { Some(v) => v, None => dflt };
}

function main(): i32 {
    var i32max: i32 = 2147483647;
    var i32min: i32 = 0 - 2147483647 - 1;

    // Annotated var destination.
    var a: Option[i32] = i32max +? 1;
    match (a) { Some(v) => { return 1; }, None => {} }
    var b: Option[i32] = 40 +? 2;
    match (b) { Some(v) => { if (v != 42) { return 2; } }, None => { return 3; } }

    // Return position, at three widths / signednesses.
    match (addc(i32max, 1))       { Some(v) => { return 4; }, None => {} }
    match (addc(40, 2))           { Some(v) => { if (v != 42) { return 5; } }, None => { return 6; } }
    match (mulc(4000000000i64, 4000000000i64)) { Some(v) => { return 7; }, None => {} }
    match (mulc(6i64, 7i64))      { Some(v) => { if (v != 42i64) { return 8; } }, None => { return 9; } }
    match (subc(10u32, 250u32))   { Some(v) => { return 10; }, None => {} }
    match (subc(250u32, 10u32))   { Some(v) => { if (v != 240u32) { return 11; } }, None => { return 12; } }

    // Call-argument position.
    if (unwrap(i32max +? 1, 99) != 99) { return 13; }
    if (unwrap(40 +? 2, 99) != 42) { return 14; }

    // Every operator in the family through an annotated destination, since the
    // guard is per-operator and a partial list would leave some mistyped.
    var d: Option[i32] = 84 /? 2;
    if (unwrap(d, 0) != 42) { return 15; }
    var e: Option[i32] = 84 /? 0;
    match (e) { Some(v) => { return 16; }, None => {} }
    var f: Option[i32] = 85 %? 43;
    if (unwrap(f, 0) != 42) { return 17; }
    var g: Option[i32] = i32min /? (0 - 1);
    match (g) { Some(v) => { return 18; }, None => {} }
    var h: Option[i32] = 1 <<? 3;
    if (unwrap(h, 0) != 8) { return 19; }
    var i: Option[i32] = 1 <<? 32;
    match (i) { Some(v) => { return 20; }, None => {} }
    var j: Option[i32] = 256 >>? 2;
    if (unwrap(j, 0) != 64) { return 21; }
    var k: Option[i32] = 256 >>? 40;
    match (k) { Some(v) => { return 22; }, None => {} }

    return 0;
}
`

func TestInterpCheckedArithDest(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = bytes.NewReader([]byte(checkedArithDestProgram))
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("interp: exit = %d, want 0 (case %d failed)\nstdout: %s\nstderr: %s",
			code, code, out.String(), errb.String())
	}
}

func TestX86_64CheckedArithDest(t *testing.T) {
	if _, code := compileAndRunX86_64(t, checkedArithDestProgram); code != 0 {
		t.Errorf("x86-64: exit = %d, want 0 (case %d failed)", code, code)
	}
}

func TestArm64CheckedArithDest(t *testing.T) {
	if _, code := compileAndRunArm64(t, checkedArithDestProgram); code != 0 {
		t.Errorf("arm64: exit = %d, want 0 (case %d failed)", code, code)
	}
}

func TestWASMCheckedArithDest(t *testing.T) {
	if code := runWasm(t, checkedArithDestProgram); code != 0 {
		t.Errorf("wasm: exit = %d, want 0 (case %d failed)", code, code)
	}
}
