package e2e

// Differential coverage for std/string `rsplit` — `split` with the pieces in
// reverse order, as if scanning from the end (`"a.b.c".rsplit(".")` is
// `["c","b","a"]`). Checks the ordinary case, no-separator (whole string as
// one piece), empty input, and an empty middle piece. Returns 42 iff every
// check holds across interp / x86-64 / wasm / arm64.

import "testing"

const stringRsplitProg = `
import "std/string";
function main(): i32 {
    var p: string[] = "a.b.c".rsplit(".");
    if (p.len() != 3 || p[0] != "c" || p[1] != "b" || p[2] != "a") { return 1; }
    var q: string[] = "hello".rsplit(".");           // no sep -> one piece
    if (q.len() != 1 || q[0] != "hello") { return 2; }
    var e: string[] = "".rsplit(",");
    if (e.len() != 1 || e[0] != "") { return 3; }
    var m: string[] = "a,,b".rsplit(",");            // ["b","","a"]
    if (m.len() != 3 || m[0] != "b" || m[1] != "" || m[2] != "a") { return 4; }
    return 42;
}
`

func TestStringRsplitInterp(t *testing.T) {
	if got := runInterpExit(t, stringRsplitProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestStringRsplitX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, stringRsplitProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestStringRsplitWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, stringRsplitProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestStringRsplitArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, stringRsplitProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
