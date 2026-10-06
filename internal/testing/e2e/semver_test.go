package e2e

import "testing"

// Differential coverage for std/semver across backends: the §11
// precedence chain (including the numeric `beta.2 < beta.11` vs lexical
// distinction), prerelease-below-release, build-metadata-ignored, and
// malformed-input rejection. Returns 42 iff every check holds. Each leg
// skips itself when its toolchain is absent.
const semverProg = `
import "std/semver" as semver;
function cmp(x: string, y: string): i32 {
    match (semver.parse(x)) {
        Some(a) => { match (semver.parse(y)) { Some(b) => { return a.compare(b); }, None => { return 99; } } },
        None => { return 99; }
    }
}
function parses(s: string): boolean {
    match (semver.parse(s)) { Some(v) => { return true; }, None => { return false; } }
}
function main(): i32 {
    if (cmp("1.0.0-alpha", "1.0.0-alpha.1") >= 0) { return 1; }
    if (cmp("1.0.0-alpha.beta", "1.0.0-beta") >= 0) { return 2; }
    if (cmp("1.0.0-beta.2", "1.0.0-beta.11") >= 0) { return 3; }   // numeric, not lexical
    if (cmp("1.0.0-rc.1", "1.0.0") >= 0) { return 4; }
    if (cmp("1.9.0", "1.10.0") >= 0) { return 5; }
    if (cmp("1.0.0+a", "1.0.0+b") != 0) { return 6; }
    if (cmp("1.2.3", "1.2.3") != 0) { return 7; }
    if (parses("1.2") || parses("1.2.3-") || parses("01.2.3") || parses("1.2.3-01")) { return 8; }
    return 42;
}
`

func TestSemverInterp(t *testing.T) {
	if got := runInterpExit(t, semverProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestSemverX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, semverProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestSemverWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, semverProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestSemverArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, semverProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
