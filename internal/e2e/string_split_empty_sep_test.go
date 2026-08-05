package e2e

// `s.split("")` char-splits on every backend.
//
// This was a live native/self-host divergence: std/string's `split` (and hence
// the interp and the native backends) char-splits an empty separator, while the
// self-host compiler lowers `s.split(sep)` to its own `__fern_str_split`
// runtime helper, which returned `[s]` instead. rt_src_str_split's comment
// recorded the gap as deliberate — it matched the hand-written self-host asm
// emitter, and no differential test covered empty-sep. The hand-asm emitters
// have since been deleted, so this is the test, and the helper now char-splits.

import "testing"

const stringSplitEmptySepProg = `
import "std/string";
function main(): i32 {
    var p: string[] = "abc".split("");
    if (p.len() != 3) { return 1; }
    if (p[0] != "a") { return 2; }
    if (p[1] != "b") { return 3; }
    if (p[2] != "c") { return 4; }

    // Empty haystack yields no pieces at all.
    if ("".split("").len() != 0) { return 5; }

    // One-byte haystack.
    var one: string[] = "z".split("");
    if (one.len() != 1 || one[0] != "z") { return 6; }

    // splitn shares the empty-sep branch and caps the piece count.
    var s2: string[] = "abcd".splitn("", 2);
    if (s2.len() != 2) { return 7; }
    if (s2[0] != "a") { return 8; }
    if (s2[1] != "bcd") { return 9; }

    // A non-empty separator is unaffected.
    if ("a,b".split(",").len() != 2) { return 10; }

    return 42;
}
`

func TestStringSplitEmptySepInterp(t *testing.T) {
	if got := runInterpExit(t, stringSplitEmptySepProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStringSplitEmptySepX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringSplitEmptySepProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStringSplitEmptySepWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringSplitEmptySepProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestStringSplitEmptySepArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringSplitEmptySepProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
