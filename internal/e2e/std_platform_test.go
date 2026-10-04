package e2e

import "testing"

// Cross-backend coverage for the capability methods of `std/platform`'s
// host platform (docs/PLATFORM-RESEARCH.md Rec §1): the log sink, the two
// clocks, the invocation environment, entropy. Each is a thin route to the
// same builtin the free-function form reaches, so what this pins is that
// the route EXISTS on every backend — a trait method on `platform.Host`,
// dispatched through modload's method hoist. Returns 42 iff every check
// holds; each leg skips itself when its toolchain is absent.
const stdPlatformProg = `
import "std/platform" as platform;
function main(): i32 {
    let plat: platform.Host = platform.host();
    if (plat.reactor != 0) { return 1; }
    plat.log("std/platform capability check");

    // Wall clock: some time after 2020-09-13, which is the last moment
    // this assertion could have been written to fail.
    let floor: i64 = 1600000000000;
    if (plat.now_ms() < floor) { return 2; }

    // Monotonic clock: non-decreasing between two readings.
    let t0: i64 = plat.elapsed_ns();
    let t1: i64 = plat.elapsed_ns();
    if (t1 < t0) { return 3; }

    // A variable nothing sets reads as None rather than "".
    match (plat.env("FERN_STD_PLATFORM_UNSET")) {
        Some(v) => { return 4; },
        None => { }
    }

    // Entropy: drawn, not pinned — the distribution is the CSPRNG's own
    // test. Three zeroes in a row is a stub, not a draw.
    if (plat.random_i32() == 0 && plat.random_i32() == 0 && plat.random_i32() == 0) { return 5; }
    return 42;
}
`

func TestStdPlatformInterp(t *testing.T) {
	if got := runInterpExit(t, stdPlatformProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStdPlatformX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stdPlatformProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStdPlatformArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stdPlatformProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

func TestStdPlatformWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stdPlatformProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}
