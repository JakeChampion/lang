package e2e

import "testing"

// Differential coverage for the std/u32 + std/u64 helpers: zero/parity
// predicates, pow (exponentiation by squaring), and the unsigned
// overflow-aware saturating_add/sub + checked_add/sub at the
// u32::MAX / u64::MAX / 0 boundaries.
//
// Split by wasmbin lowering: the pure-unsigned parts and the
// Option[u32]-returning checked_* run on all four backends (an i32-width
// enum payload lowers fine); the Option[u64] checked_* variants run on
// interp / x86-64 / arm64 only, since an enum with a u64 payload is the
// same known wasmbin coverage gap as Option[i64].
const unsignedHelpersProg = `
import "std/u32";
import "std/u64";
function main(): i32 {
    var M64: u64 = 18446744073709551615 as u64;
    var M32: u32 = 4294967295 as u32;
    if (!(0 as u64).is_zero() || !(4 as u64).is_even() || !(3 as u64).is_odd()) { return 1; }
    if ((2 as u64).pow(40) != (1099511627776 as u64)) { return 2; }
    if (M64.saturating_add(1 as u64) != M64) { return 3; }
    if ((3 as u64).saturating_sub(10 as u64) != (0 as u64)) { return 4; }
    if ((10 as u64).saturating_add(5 as u64) != (15 as u64)) { return 5; }
    if ((10 as u64).saturating_sub(4 as u64) != (6 as u64)) { return 6; }
    if (!(0 as u32).is_zero() || !(4 as u32).is_even() || !(3 as u32).is_odd()) { return 7; }
    if ((2 as u32).pow(10) != (1024 as u32)) { return 8; }
    if (M32.saturating_add(1 as u32) != M32) { return 9; }
    if ((3 as u32).saturating_sub(10 as u32) != (0 as u32)) { return 10; }
    if ((10 as u32).saturating_add(5 as u32) != (15 as u32)) { return 11; }
    return 42;
}
`

const u32CheckedProg = `
import "std/u32";
function opt(o: Option[u32], fallback: u32): u32 {
    match (o) { Some(v) => { return v; }, None => { return fallback; } }
}
function main(): i32 {
    var M32: u32 = 4294967295 as u32;
    if (opt(M32.checked_add(1 as u32), 99 as u32) != (99 as u32)) { return 1; }
    if (opt((3 as u32).checked_add(4 as u32), 99 as u32) != (7 as u32)) { return 2; }
    if (opt((3 as u32).checked_sub(10 as u32), 99 as u32) != (99 as u32)) { return 3; }
    if (opt((9 as u32).checked_sub(4 as u32), 99 as u32) != (5 as u32)) { return 4; }
    return 42;
}
`

const u64CheckedProg = `
import "std/u64";
function opt(o: Option[u64], fallback: u64): u64 {
    match (o) { Some(v) => { return v; }, None => { return fallback; } }
}
function main(): i32 {
    var M64: u64 = 18446744073709551615 as u64;
    if (opt(M64.checked_add(1 as u64), 99 as u64) != (99 as u64)) { return 1; }
    if (opt((3 as u64).checked_add(4 as u64), 99 as u64) != (7 as u64)) { return 2; }
    if (opt((3 as u64).checked_sub(10 as u64), 99 as u64) != (99 as u64)) { return 3; }
    if (opt((9 as u64).checked_sub(4 as u64), 99 as u64) != (5 as u64)) { return 4; }
    return 42;
}
`

func TestUnsignedHelpersInterp(t *testing.T) {
	if got := runInterpExit(t, unsignedHelpersProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestUnsignedHelpersX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, unsignedHelpersProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestUnsignedHelpersWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, unsignedHelpersProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestUnsignedHelpersArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, unsignedHelpersProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

func TestU32CheckedInterp(t *testing.T) {
	if got := runInterpExit(t, u32CheckedProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestU32CheckedX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, u32CheckedProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestU32CheckedWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, u32CheckedProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestU32CheckedArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, u32CheckedProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

func TestU64CheckedInterp(t *testing.T) {
	if got := runInterpExit(t, u64CheckedProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestU64CheckedX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, u64CheckedProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestU64CheckedArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, u64CheckedProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
