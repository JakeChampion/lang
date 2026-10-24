package e2e

import (
	"testing"
)

const structFieldStealBody = `
import "std/i32";

struct View { shape: i32[], strides: i32[] }

function make(): View {
    let st: i32[] = [];
    st = st.append(4);
    st = st.append(1);
    return View { shape: [3, 4], strides: st };
}

function (a: View) flip(axis: i32): View {
    return View { shape: a.shape, strides: a.strides.with(axis, 0 - a.strides[axis]) };
}

function (a: View) zip(b: View): i32 {
    return a.strides[0] + b.strides[0] + a.shape[0] + b.shape[1];
}

function verdict(): i32 {
    let a: View = make();
    if (a.zip(a.flip(1)) != 15) { return 1; }
    // The same shape written as a free call, which lowers the receiver as an
    // ordinary argument and crashed identically.
    let b: View = make();
    if (zipf(b, b.flip(1)) != 15) { return 2; }
    // A view bound to a local first always worked; it is here so a fix that
    // withdrew too many deaths still has to keep this answering.
    let c: View = make();
    let v: View = c.flip(1);
    if (c.zip(v) != 15) { return 3; }
    return 42;
}

function zipf(a: View, b: View): i32 {
    return a.strides[0] + b.strides[0] + a.shape[0] + b.shape[1];
}
`

const structFieldStealSrc = structFieldStealBody + `
function main(): i32 { return verdict(); }
`

func TestInterpStructFieldSteal(t *testing.T) {
	if got := runInterpExit(t, structFieldStealSrc); got != 42 {
		t.Errorf("interp = %d, want 42 (1/2 = a field was stolen from a live struct, 3 = the bound form regressed)", got)
	}
}

func TestX86_64StructFieldSteal(t *testing.T) {
	if _, got := compileAndRunX86_64(t, structFieldStealSrc); got != 42 {
		t.Errorf("x86-64 = %d, want 42 (1/2 = a field was stolen from a live struct, 3 = the bound form regressed)", got)
	}
}

func TestArm64StructFieldSteal(t *testing.T) {
	if _, got := compileAndRunArm64(t, structFieldStealSrc); got != 42 {
		t.Errorf("arm64 = %d, want 42 (1/2 = a field was stolen from a live struct, 3 = the bound form regressed)", got)
	}
}

func TestWASMStructFieldSteal(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, structFieldStealSrc); got != 42 {
		t.Errorf("wasm = %d, want 42 (1/2 = a field was stolen from a live struct, 3 = the bound form regressed)", got)
	}
}
