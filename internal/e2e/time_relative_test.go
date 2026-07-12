package e2e

import "testing"

// Differential coverage for std/time.Instant.relative_to across
// backends: the "just now" window either side of the reference, past
// vs future direction ("ago" / "in"), singular vs plural units, and the
// unit ladder from seconds up through years. Returns 42 iff every
// exact-string check holds. Each leg skips itself when its toolchain is
// absent.
const timeRelativeProg = `
import "std/time" as time;
function at(sec: i64): Instant { return time.instant_from_unix(sec); }
function main(): i32 {
    var now: Instant = at(1000000 as i64);
    if (at(1000000 as i64).relative_to(now) != "just now") { return 1; }
    if (at(999997 as i64).relative_to(now) != "just now") { return 2; }
    if (at(999990 as i64).relative_to(now) != "10 seconds ago") { return 3; }
    if (at(1000030 as i64).relative_to(now) != "in 30 seconds") { return 4; }
    if (at(999940 as i64).relative_to(now) != "1 minute ago") { return 5; }
    if (at(999880 as i64).relative_to(now) != "2 minutes ago") { return 6; }
    if (at(996400 as i64).relative_to(now) != "1 hour ago") { return 7; }
    if (at(1010800 as i64).relative_to(now) != "in 3 hours") { return 8; }
    if (at(913600 as i64).relative_to(now) != "1 day ago") { return 9; }
    if (at(1000000 + 5 * 86400 as i64).relative_to(now) != "in 5 days") { return 10; }
    if (at(1000000 - 40 * 86400 as i64).relative_to(now) != "1 month ago") { return 11; }
    if (at(1000000 - 400 * 86400 as i64).relative_to(now) != "1 year ago") { return 12; }
    if (at(1000000 + 800 * 86400 as i64).relative_to(now) != "in 2 years") { return 13; }
    return 42;
}
`

func TestTimeRelativeInterp(t *testing.T) {
	if got := runInterpExit(t, timeRelativeProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestTimeRelativeX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, timeRelativeProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestTimeRelativeWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, timeRelativeProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestTimeRelativeArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, timeRelativeProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
