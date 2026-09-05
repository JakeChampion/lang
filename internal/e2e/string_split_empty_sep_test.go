package e2e

// `s.split("")` splits into CODEPOINTS on every backend.
//
// This was a live native/self-host divergence: std/string's `split` (and hence
// the interp and the native backends) char-splits an empty separator, while the
// self-host compiler lowers `s.split(sep)` to its own `__fern_str_split`
// runtime helper, which returned `[s]` instead. rt_src_str_split's comment
// recorded the gap as deliberate — it matched the hand-written self-host asm
// emitter, and no differential test covered empty-sep. The hand-asm emitters
// have since been deleted, so this is the test, and the helper now char-splits.
//
// The unit is a codepoint, not a byte (#8469): every piece is a whole
// character, so no fragment is ever invalid UTF-8. `to_array()` is the
// per-byte split for callers that want one.

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

    // Non-ASCII: one piece per codepoint, each piece its whole encoding.
    // "héllo" is 6 bytes, 5 characters.
    var h: string[] = "héllo".split("");
    if ("héllo".len() != 6) { return 11; }
    if (h.len() != 5) { return 12; }
    if (h[1] != "é" || h[1].len() != 2) { return 13; }
    if (h[4] != "o") { return 14; }

    // A 4-byte codepoint stays one piece.
    var e: string[] = "a😀b".split("");
    if (e.len() != 3) { return 15; }
    if (e[1] != "😀" || e[1].len() != 4) { return 16; }

    // splitn's empty-sep branch steps in the same units: the tail keeps
    // the rest of the bytes intact.
    var sn: string[] = "héllo".splitn("", 2);
    if (sn.len() != 2) { return 17; }
    if (sn[0] != "h" || sn[1] != "éllo") { return 18; }

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
