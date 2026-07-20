package e2e

import "testing"

// Differential coverage for the integer log10_floor helpers — std/i32,
// std/i64, std/u32, std/u64 — the base-10 sibling of log2_floor and the
// digit-count primitive (a positive n has n.log10_floor()+1 decimal digits).
// Each returns floor(log10 n) for n >= 1 and the -1 "no real log" sentinel for
// n <= 0 (n == 0 for the unsigned types). Counting divisions by ten stays exact
// where a float log10 would round near a power of ten, and — like log2_floor —
// needs no leading-zeros primitive. The unsigned cases cross 2^31 / the i64
// range so a signed divide/compare would give the wrong answer, mirroring the
// unsigned guards in u32_roots / u64_roots. Returns 42 iff every check holds
// across interp / x86-64 / wasm / arm64; each leg skips itself when its
// toolchain is absent.
const intLog10Prog = `
import "std/i32";
import "std/i64" as i64m;
import "std/u32" as u32m;
import "std/u64" as u64m;
function main(): i32 {
    // i32: floor(log10) across the digit boundaries, i32-max (10 digits), and
    // the <= 0 sentinel (zero and negative).
    if ((1).log10_floor() != 0) { return 1; }
    if ((9).log10_floor() != 0) { return 2; }
    if ((10).log10_floor() != 1) { return 3; }
    if ((99).log10_floor() != 1) { return 4; }
    if ((100).log10_floor() != 2) { return 5; }
    if ((999).log10_floor() != 2) { return 6; }
    if ((2147483647).log10_floor() != 9) { return 7; }        // i32 max, 10 digits
    if ((0).log10_floor() != (0 - 1)) { return 8; }           // sentinel
    if ((0 - 42).log10_floor() != (0 - 1)) { return 9; }      // negative sentinel
    // i64: exact past the i32 range.
    if ((1 as i64).log10_floor() != 0) { return 10; }
    if ((999999999999 as i64).log10_floor() != 11) { return 11; }   // twelve 9s
    if ((1000000000000 as i64).log10_floor() != 12) { return 12; }  // 10^12, 13 digits
    if ((0 as i64).log10_floor() != (0 - 1)) { return 13; }
    // u32: a value above 2^31 (signed-negative) — a signed divide/compare would
    // truncate the loop early and return 0.
    if ((1 as u32).log10_floor() != 0) { return 14; }
    if ((4000000000 as u32).log10_floor() != 9) { return 15; }      // > 2^31, 10 digits
    if ((0 as u32).log10_floor() != (0 - 1)) { return 16; }
    // u64: exact past the signed i64 range (10^19 has 20 digits).
    if ((1 as u64).log10_floor() != 0) { return 17; }
    if ((10000000000000000000 as u64).log10_floor() != 19) { return 18; }
    if ((0 as u64).log10_floor() != (0 - 1)) { return 19; }
    return 42;
}
`

func TestIntLog10Interp(t *testing.T) {
	if got := runInterpExit(t, intLog10Prog); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestIntLog10X86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, intLog10Prog); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestIntLog10Wasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, intLog10Prog); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestIntLog10Arm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, intLog10Prog); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
