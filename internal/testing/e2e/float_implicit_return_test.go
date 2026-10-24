package e2e

import "testing"

const floatImplicitReturnProg = `
// An f64 match with no fall-through arm -- the shape that broke.
function unwrap64(o: Option[f64]): f64 {
    match (o) { Some(v) => { return v; }, None => { return 0.0 - 999.0; } }
}

// An if/else with returns in both arms and nothing after it -- same
// fall-off-the-end shape without a match.
function pick64(flag: boolean, a: f64, b: f64): f64 {
    if (flag) { return a; } else { return b; }
}

// The f32 mirror, so the fix cannot be "always emit f64". Deliberately NOT
// routed through Option[f32]: that shape has a separate, pre-existing wasm
// gap (an f32 enum payload reads back through an i32 slot -- "expected i32,
// found f32"), which would mask this case rather than cover it.
function pick32(flag: boolean, a: f32, b: f32): f32 {
    if (flag) { return a; } else { return b; }
}

function mk64(xs: f64[]): Option[f64] {
    if (xs.len() == 0) { return None; }
    return Some(xs[0]);
}

function near64(a: f64, b: f64): boolean {
    let d: f64 = a - b;
    if (d < 0.0) { d = 0.0 - d; }
    return d < 0.0001;
}

function near32(a: f32, b: f32): boolean {
    let d: f64 = (a as f64) - (b as f64);
    if (d < 0.0) { d = 0.0 - d; }
    return d < 0.001;
}

function main(): i32 {
    let xs: f64[] = [3.5];
    if (!near64(unwrap64(mk64(xs)), 3.5)) { return 1; }
    let empty: f64[] = [];
    if (!near64(unwrap64(mk64(empty)), 0.0 - 999.0)) { return 2; }

    if (!near64(pick64(true, 1.5, 2.5), 1.5)) { return 3; }
    if (!near64(pick64(false, 1.5, 2.5), 2.5)) { return 4; }

    if (!near32(pick32(true, 2.25 as f32, 1.0 as f32), 2.25 as f32)) { return 5; }
    if (!near32(pick32(false, 2.25 as f32, 1.0 as f32), 1.0 as f32)) { return 6; }

    return 42;
}
`

func TestFloatImplicitReturnInterp(t *testing.T) {
	if got := runInterpExit(t, floatImplicitReturnProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestFloatImplicitReturnX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, floatImplicitReturnProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestFloatImplicitReturnWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, floatImplicitReturnProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestFloatImplicitReturnArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, floatImplicitReturnProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
