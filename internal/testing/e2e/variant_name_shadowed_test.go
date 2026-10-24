package e2e

import "testing"

const variantNameShadowedProg = `
// Shadows the built-in Ok. Returns Err deliberately, so the two candidate
// semantics give different answers: user function -> Err(v) -> 4;
// built-in constructor -> Ok(v) -> 104.
function Ok(v: i32): Result[i32, i32] { return Err(v); }

// Shadows the built-in Some the same way.
function Some(v: i32): Option[i32] { return None; }

// Call-site position: Ok(4) as a let initialiser.
function call_site(): i32 {
    let got: Result[i32, i32] = Ok(4);
    match (got) { Ok(v) => { return 100 + v; }, Err(e) => { return e; } }
}

// Return position: routes through the pair-form path instead.
function mk(v: i32): Result[i32, i32] { return Ok(v); }

function return_site(): i32 {
    match (mk(4)) { Ok(v) => { return 100 + v; }, Err(e) => { return e; } }
}

// The Option pair: shadowed Some returns None, so the None arm must run.
function option_site(): i32 {
    let got: Option[i32] = Some(9);
    match (got) { Some(v) => { return 100 + v; }, None => { return 7; } }
}

function mk_opt(v: i32): Option[i32] { return Some(v); }

function option_return_site(): i32 {
    match (mk_opt(9)) { Some(v) => { return 100 + v; }, None => { return 7; } }
}

// Controls: Err and None are NOT shadowed, so they must still construct.
function unshadowed_ctor(): i32 {
    let e: Result[i32, i32] = Err(5);
    match (e) { Ok(v) => { return 100 + v; }, Err(x) => { return x; } }
}


function unshadowed_nullary(): i32 {
    let o: Option[i32] = None;
    match (o) { Some(v) => { return 100 + v; }, None => { return 3; } }
}

function main(): i32 {
    if (call_site() != 4) { return 1; }
    if (return_site() != 4) { return 2; }
    if (option_site() != 7) { return 3; }
    if (option_return_site() != 7) { return 4; }
    if (unshadowed_ctor() != 5) { return 5; }
    if (unshadowed_nullary() != 3) { return 6; }
    return 42;
}
`

const variantNameUnshadowedProg = `
function mk_ok(v: i32): Result[i32, i32] { return Ok(v); }
function mk_err(v: i32): Result[i32, i32] { return Err(v); }
function mk_some(v: i32): Option[i32] { return Some(v); }
function mk_none(): Option[i32] { return None; }

function main(): i32 {
    match (mk_ok(1)) { Ok(v) => { if (v != 1) { return 1; } }, Err(e) => { return 11; } }
    match (mk_err(2)) { Ok(v) => { return 12; }, Err(e) => { if (e != 2) { return 2; } } }
    match (mk_some(3)) { Some(v) => { if (v != 3) { return 3; } }, None => { return 13; } }
    match (mk_none()) { Some(v) => { return 14; }, None => { } }
    let direct: Result[i32, i32] = Ok(8);
    match (direct) { Ok(v) => { if (v != 8) { return 4; } }, Err(e) => { return 15; } }
    return 42;
}
`

const variantNullaryShadowedProg = `
function None(v: i32): Option[i32] { return Some(v); }

function mk_nullary(v: i32): Option[i32] { return None(v); }

function main(): i32 {
    match (mk_nullary(5)) { Some(v) => { return v; }, None => { return 99; } }
}
`

func TestVariantNullaryNameShadowedInterp(t *testing.T) {
	if got := runInterpExit(t, variantNullaryShadowedProg); got != 5 {
		t.Fatalf("interp got %d, want 5 (99 = the shadowed nullary name lowered as the built-in constructor)", got)
	}
}

func TestVariantNullaryNameShadowedX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, variantNullaryShadowedProg); got != 5 {
		t.Fatalf("x86-64 got %d, want 5 (99 = the shadowed nullary name lowered as the built-in constructor)", got)
	}
}

func TestVariantNullaryNameShadowedArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, variantNullaryShadowedProg); got != 5 {
		t.Fatalf("arm64 got %d, want 5 (99 = the shadowed nullary name lowered as the built-in constructor)", got)
	}
}

func TestVariantNullaryNameShadowedWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, variantNullaryShadowedProg); got != 5 {
		t.Fatalf("wasm got %d, want 5 (99 = the shadowed nullary name lowered as the built-in constructor)", got)
	}
}

const shadowWant = "want 42 (1 = call site constructed instead of calling, " +
	"2 = pair-form return did, 3/4 = same for Option, 5/6 = an UNSHADOWED constructor stopped working)"

func TestVariantNameShadowedByFuncInterp(t *testing.T) {
	if got := runInterpExit(t, variantNameShadowedProg); got != 42 {
		t.Fatalf("interp got %d, %s", got, shadowWant)
	}
}

func TestVariantNameShadowedByFuncX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, variantNameShadowedProg); got != 42 {
		t.Fatalf("x86-64 got %d, %s", got, shadowWant)
	}
}

func TestVariantNameShadowedByFuncArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, variantNameShadowedProg); got != 42 {
		t.Fatalf("arm64 got %d, %s", got, shadowWant)
	}
}

func TestVariantNameShadowedByFuncWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, variantNameShadowedProg); got != 42 {
		t.Fatalf("wasm got %d, %s", got, shadowWant)
	}
}

func TestVariantNameUnshadowedStillConstructsInterp(t *testing.T) {
	if got := runInterpExit(t, variantNameUnshadowedProg); got != 42 {
		t.Fatalf("interp got %d, want 42 (an unshadowed built-in constructor changed behaviour)", got)
	}
}

func TestVariantNameUnshadowedStillConstructsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, variantNameUnshadowedProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (an unshadowed built-in constructor changed behaviour)", got)
	}
}

func TestVariantNameUnshadowedStillConstructsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, variantNameUnshadowedProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (an unshadowed built-in constructor changed behaviour)", got)
	}
}

func TestVariantNameUnshadowedStillConstructsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, variantNameUnshadowedProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (an unshadowed built-in constructor changed behaviour)", got)
	}
}
