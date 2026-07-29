package e2e

// Differential coverage for std/string `rsplitn` — `splitn` scanning from the
// END: at most n pieces, in reverse order, with the FIRST (leftmost) piece
// carrying the unsplit head. Mirrors Rust's `str::rsplitn`; pairs with
// `splitn` / `rsplit`. Returns 42 iff every check holds across interp /
// x86-64 / wasm / arm64.

import "testing"

const stringRsplitnProg = `
import "std/string";
function main(): i32 {
    var r2: string[] = "a.b.c.d".rsplitn(".", 2);
    if (r2.len() != 2 || r2[0] != "d" || r2[1] != "a.b.c") { return 1; }
    var r3: string[] = "a.b.c.d".rsplitn(".", 3);
    if (r3.len() != 3 || r3[0] != "d" || r3[1] != "c" || r3[2] != "a.b") { return 2; }
    var r1: string[] = "a.b.c.d".rsplitn(".", 1);
    if (r1.len() != 1 || r1[0] != "a.b.c.d") { return 3; }
    // Fewer pieces than the cap behaves as a full rsplit.
    var few: string[] = "a.b".rsplitn(".", 5);
    if (few.len() != 2 || few[0] != "b" || few[1] != "a") { return 4; }
    // n <= 0 -> empty.
    if ("x.y".rsplitn(".", 0).len() != 0) { return 5; }
    // No separator: one piece.
    var none: string[] = "hello".rsplitn(".", 3);
    if (none.len() != 1 || none[0] != "hello") { return 6; }
    // Empty middle field survives ("a,,b" from the right, n=3).
    var mid: string[] = "a,,b".rsplitn(",", 3);
    if (mid.len() != 3 || mid[0] != "b" || mid[1] != "" || mid[2] != "a") { return 7; }
    return 42;
}
`

func TestStringRsplitnInterp(t *testing.T) {
	if got := runInterpExit(t, stringRsplitnProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStringRsplitnX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringRsplitnProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStringRsplitnWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringRsplitnProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestStringRsplitnArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringRsplitnProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
