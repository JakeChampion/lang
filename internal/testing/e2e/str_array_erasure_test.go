package e2e

import "testing"

const strArrayProgram = `
function main(): i32 {
    // A str[] literal: construction alone used to crash.
    let o: str[] = ["a", "bb"];
    if (o.len() != 2) { return 1; }
    // ... and so did reading the elements back.
    if (o[0].len() != 1) { return 2; }
    if (o[1].len() != 2) { return 3; }

    // The owned spelling must stay identical — the control that always
    // passed, kept so a future change cannot "fix" str[] by breaking it.
    let s: string[] = ["a", "bb"];
    if (s.len() != 2) { return 4; }
    if (s[0].len() != 1) { return 5; }
    if (s[1].len() != 2) { return 6; }

    // A str element concatenates like the string it erases to. This is
    // the shape that actually freed the backing storage.
    let out: string = "";
    out = out + o[0];
    out = out + o[1];
    if (out != "abb") { return 7; }

    // Slicing a string yields str views; holding several is the shape
    // std/unicode's segmentation wanted and could not have.
    let src: string = "hello";
    let parts: str[] = [slice_unchecked(src, 0, 2), slice_unchecked(src, 2, 5)];
    if (parts.len() != 2) { return 8; }
    if (parts[0].len() != 2) { return 9; }
    if (parts[1].len() != 3) { return 10; }
    if (parts[0] + parts[1] != "hello") { return 11; }

    // char[] must be unaffected: char is classified at pointer width by
    // these same slots and every other stride site agrees with that, so
    // erasing it here too would break char[] exactly as this fixes str[].
    let cs: char[] = [65 as char, 66 as char];
    if (cs.len() != 2) { return 12; }
    if (cs[1] as i32 != 66) { return 13; }
    return 0;
}
`

func TestStrArrayErasureInterp(t *testing.T) {
	if got := runInterpExit(t, strArrayProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestStrArrayErasureX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, strArrayProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestStrArrayErasureWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, strArrayProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestStrArrayErasureArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, strArrayProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
