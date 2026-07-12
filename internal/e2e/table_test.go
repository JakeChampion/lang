package e2e

import "testing"

// Differential coverage for std/table across backends: column-aligned
// render (last column unpadded), short-row padding, code-point-width
// alignment, and the header variant with its rule. Returns 42 iff every
// exact-string check holds. Each leg skips itself when its toolchain is
// absent.
const tableProg = `
import "std/table" as table;
function main(): i32 {
    if (table.render([["a", "bb"], ["ccc", "d"]]) != "a    bb\nccc  d") { return 1; }
    if (table.render([["x", "y", "z"], ["1"]]) != "x  y  z\n1     ") { return 2; }
    if (table.render([["café", "x"], ["a", "y"]]) != "café  x\na     y") { return 3; }
    if (table.render([["only"], ["one"]]) != "only\none") { return 4; }
    var empty: string[][] = [];
    if (table.render(empty) != "") { return 5; }
    if (table.render_with_header(["name", "age"], [["ada", "36"], ["bo", "9"]]) != "name  age\n----  ---\nada   36\nbo    9") { return 6; }
    return 42;
}
`

func TestTableInterp(t *testing.T) {
	if got := runInterpExit(t, tableProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestTableX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, tableProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestTableWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, tableProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestTableArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, tableProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}
