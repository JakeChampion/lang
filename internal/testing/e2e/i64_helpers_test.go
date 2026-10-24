package e2e

import "testing"

const i64HelpersProg = `
import "std/i64";
function main(): i32 {
    let MAX: i64 = 9223372036854775807 as i64;
    let MIN: i64 = (0 as i64) - 9223372036854775807 - 1;
    if ((5 as i64).signum() != (1 as i64)) { return 1; }
    if (((0 as i64) - 5).signum() != (0 as i64) - 1) { return 2; }
    if ((0 as i64).signum() != (0 as i64)) { return 3; }
    if (!(5 as i64).is_positive()) { return 4; }
    if (!((0 as i64) - 5).is_negative()) { return 5; }
    if (!(0 as i64).is_zero()) { return 6; }
    if ((0 as i64).is_positive()) { return 7; }
    if (MAX.saturating_add(1 as i64) != MAX) { return 8; }
    if (MIN.saturating_add((0 as i64) - 1) != MIN) { return 9; }
    if ((10 as i64).saturating_add(5 as i64) != (15 as i64)) { return 10; }
    if (MIN.saturating_sub(1 as i64) != MIN) { return 11; }
    if (MAX.saturating_sub((0 as i64) - 1) != MAX) { return 12; }
    if ((10 as i64).saturating_sub(3 as i64) != (7 as i64)) { return 13; }
    return 42;
}
`

const i64CheckedProg = `
import "std/i64";
function opt(o: Option[i64], fallback: i64): i64 {
    match (o) { Some(v) => { return v; }, None => { return fallback; } }
}
function main(): i32 {
    let MAX: i64 = 9223372036854775807 as i64;
    let MIN: i64 = (0 as i64) - 9223372036854775807 - 1;
    if (opt(MAX.checked_add(1 as i64), (0 as i64) - 99) != (0 as i64) - 99) { return 1; }
    if (opt((3 as i64).checked_add(4 as i64), (0 as i64) - 99) != (7 as i64)) { return 2; }
    if (opt(MIN.checked_sub(1 as i64), (0 as i64) - 99) != (0 as i64) - 99) { return 3; }
    if (opt((9 as i64).checked_sub(4 as i64), (0 as i64) - 99) != (5 as i64)) { return 4; }
    return 42;
}
`

func TestI64HelpersInterp(t *testing.T) {
	if got := runInterpExit(t, i64HelpersProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestI64HelpersX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, i64HelpersProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestI64HelpersWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, i64HelpersProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestI64HelpersArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, i64HelpersProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

func TestI64CheckedInterp(t *testing.T) {
	if got := runInterpExit(t, i64CheckedProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestI64CheckedX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, i64CheckedProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestI64CheckedArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, i64CheckedProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
