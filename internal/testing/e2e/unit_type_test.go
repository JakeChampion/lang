package e2e

import "testing"

const unitProgram = `
function ok(): Result[void, IoError] { return Ok(()); }
function bad(): Result[void, IoError] { return Err(Interrupted); }

function chain(fail: boolean): Result[void, IoError] {
    ok()?;
    if (fail) { bad()?; }
    return Ok(());
}

function main(): i32 {
    let n: i32 = 0;
    match (chain(false)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 10; } }
    match (chain(true))  { Ok(_) => { n = n + 100; }, Err(_) => { n = n + 2; } }
    return n;
}`

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

func TestUnitTypeWasm(t *testing.T) {
	if code := runWasm(t, unitProgram); code != 3 {
		t.Errorf("wasm: got exit %d, want 3", code)
	}
}
