package e2e

import "testing"

// Differential coverage for std/string.slugify across backends:
// lower-casing, non-alphanumeric runs collapsing to a single hyphen,
// leading/trailing separator trimming, underscores and dots as
// separators, digit preservation, non-ASCII bytes acting as separators
// (dropped, not transliterated), and the empty / all-separator results.
// Returns 42 iff every check holds. Each leg skips itself when its
// toolchain is absent.
const slugifyProg = `
import "std/string";
function main(): i32 {
    if ("Hello, World! 2024".slugify() != "hello-world-2024") { return 1; }
    if ("  --Foo__Bar--  ".slugify() != "foo-bar") { return 2; }
    if ("already-a-slug".slugify() != "already-a-slug") { return 3; }
    if ("MixedCASE Text".slugify() != "mixedcase-text") { return 4; }
    if ("multiple   spaces".slugify() != "multiple-spaces") { return 5; }
    if ("trailing punctuation!!!".slugify() != "trailing-punctuation") { return 6; }
    if ("!!!leading".slugify() != "leading") { return 7; }
    if ("".slugify() != "") { return 8; }
    if ("!!!".slugify() != "") { return 9; }
    if ("café society".slugify() != "caf-society") { return 10; }
    if ("under_score".slugify() != "under-score") { return 11; }
    if ("a.b.c".slugify() != "a-b-c") { return 12; }
    if ("123 numbers 456".slugify() != "123-numbers-456") { return 13; }
    return 42;
}
`

func TestSlugifyInterp(t *testing.T) {
	if got := runInterpExit(t, slugifyProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestSlugifyX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, slugifyProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestSlugifyWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, slugifyProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestSlugifyArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, slugifyProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
