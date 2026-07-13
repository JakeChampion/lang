package e2e

import "testing"

// Differential coverage for std/time.Duration.to_string across backends:
// the zero case, single and multi-unit durations, zero-unit skipping
// (1h5s), millisecond rendering, negatives, and sub-millisecond rounding
// to "0s". Returns 42 iff every check holds. Each leg skips itself when
// its toolchain is absent.
const durationToStringProg = `
import "std/time" as time;
function main(): i32 {
    if (time.duration_seconds(0 as i64).to_string() != "0s") { return 1; }
    if (time.duration_seconds(45 as i64).to_string() != "45s") { return 2; }
    if (time.duration_seconds(90 as i64).to_string() != "1m30s") { return 3; }
    if (time.duration_seconds(3600 as i64).to_string() != "1h") { return 4; }
    if (time.duration_seconds(3661 as i64).to_string() != "1h1m1s") { return 5; }
    if (time.duration_seconds(3605 as i64).to_string() != "1h5s") { return 6; }
    if (time.duration_millis(500 as i64).to_string() != "500ms") { return 7; }
    if (time.duration_millis(1500 as i64).to_string() != "1s500ms") { return 8; }
    if (time.duration_millis(3661500 as i64).to_string() != "1h1m1s500ms") { return 9; }
    if (time.duration_seconds((0 as i64) - 10).to_string() != "-10s") { return 10; }
    if (time.duration_millis((0 as i64) - 500).to_string() != "-500ms") { return 11; }
    if (time.duration_seconds((0 as i64) - 3661).to_string() != "-1h1m1s") { return 12; }
    var subms: Duration = Duration { sec: 0 as i64, nsec: 500000 };
    if (subms.to_string() != "0s") { return 13; }
    if (time.duration_seconds(172800 as i64).to_string() != "48h") { return 14; }
    return 42;
}
`

func TestDurationToStringInterp(t *testing.T) {
	if got := runInterpExit(t, durationToStringProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestDurationToStringX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, durationToStringProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestDurationToStringWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, durationToStringProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestDurationToStringArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, durationToStringProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
