package e2e

import "testing"

// The unit value `()` is the payload of a Result that succeeded with
// nothing to report — `Result[void, IoError]`, the shape a fallible
// operation like a file write returns. What matters at runtime is that
// the payload occupies a real slot: `Ok(())` has to construct, match, and
// propagate through `?` identically on every backend.
//
// unitProgram exercises all three in one program. `chain` calls `?` on a
// unit Result twice — once on the Ok path (the payload is unwrapped and
// discarded) and once on the Err path (the error propagates) — so a
// backend that mislays the payload slot diverges on the exit code rather
// than merely on the shape.
//
//	success path  → +1
//	failure path  → +2
//	exit          → 3
const unitProgram = `
function ok(): Result[(), IoError] { return Ok(()); }
function bad(): Result[(), IoError] { return Err(Interrupted); }

function chain(fail: boolean): Result[(), IoError] {
    ok()?;
    if (fail) { bad()?; }
    return Ok(());
}

function main(): i32 {
    var n: i32 = 0;
    match (chain(false)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 10; } }
    match (chain(true))  { Ok(_) => { n = n + 100; }, Err(_) => { n = n + 2; } }
    return n;
}`

// The interpreter is the reference the compiled backends are checked
// against.
func TestUnitTypeInterp(t *testing.T) {
	if code := interpExit(t, buildLangBinForInterp(t), unitProgram); code != 3 {
		t.Errorf("interp: got exit %d, want 3", code)
	}
}

func TestUnitTypeX86_64(t *testing.T) {
	if _, code := compileAndRunX86_64(t, unitProgram); code != 3 {
		t.Errorf("x86-64: got exit %d, want 3", code)
	}
}

func TestUnitTypeArm64(t *testing.T) {
	if _, code := compileAndRunArm64(t, unitProgram); code != 3 {
		t.Errorf("arm64: got exit %d, want 3", code)
	}
}

// wasm is the backend this guards hardest. `valtypeFor` had no VoidType
// case, so a void-typed payload was an "unsupported type" hard error; the
// naive fix (map it to i32) then produced a module that failed
// verification, because a void-returning *call* pushes nothing for the
// payload store to consume. The literal is a constant, so it pushes — and
// E072 keeps the call form from reaching codegen at all.
func TestUnitTypeWasm(t *testing.T) {
	if code := runWasm(t, unitProgram); code != 3 {
		t.Errorf("wasm: got exit %d, want 3", code)
	}
}
