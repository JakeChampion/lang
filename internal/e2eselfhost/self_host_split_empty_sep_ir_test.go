package e2eselfhost

import "testing"

// splitEmptySepSrc splits on an empty separator, which cuts a string into
// codepoints on every backend. The wasm runtime's hand-written
// $__fern_str_split cut it into bytes, so "héllo" came back as six pieces
// (#10770). The program prints 42 once every check holds; a lower number
// names the first that failed.
const splitEmptySepSrc = `
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

    print("42");
    return 0;
}
`

func TestSelfHostSplitEmptySepIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	if want, code := runInterp(t, splitEmptySepSrc); code != 0 || want != "42\n" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", splitEmptySepSrc)); code != 0 || out != "42\n" {
		t.Fatalf("exit %d, stdout %q; want 0, \"42\"", code, out)
	}
}

func TestSelfHostSplitEmptySepIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", splitEmptySepSrc)); code != 0 || out != "42\n" {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, \"42\"", code, out)
	}
}

func TestSelfHostSplitEmptySepWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", splitEmptySepSrc)); code != 0 || out != "42\n" {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, \"42\"", code, out)
	}
}
