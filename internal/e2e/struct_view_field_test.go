package e2e

import "testing"

// structViewFieldProgram reads fields through the elements of a struct-array
// view (`[P]`): a local view, a view parameter, and a string field (#10251).
// Exits 0 on success, a distinct code per failed step.
const structViewFieldProgram = `struct P { x: i32, name: string }

function second_x(qs: [P]): i32 {
    return qs[1].x;
}

function main(): i32 {
    var ps: P[] = [P { x: 1, name: "one" }, P { x: 2, name: "two" }, P { x: 3, name: "three" }];
    var qs: [P] = ps[1:3];
    if (qs[0].x != 2) { return 1; }
    if (qs[1].name.len() != 5) { return 2; }
    if (second_x(ps[0:2]) != 2) { return 3; }
    if (second_x(qs) != 3) { return 4; }
    return 0;
}
`

func TestInterpStructViewField(t *testing.T) {
	if code := runInterpExit(t, structViewFieldProgram); code != 0 {
		t.Errorf("interp struct view field: exit = %d, want 0", code)
	}
}

func TestX86_64StructViewField(t *testing.T) {
	if _, code := compileAndRunX86_64(t, structViewFieldProgram); code != 0 {
		t.Errorf("x86-64 struct view field: exit = %d, want 0", code)
	}
}

func TestArm64StructViewField(t *testing.T) {
	if _, code := compileAndRunArm64(t, structViewFieldProgram); code != 0 {
		t.Errorf("arm64 struct view field: exit = %d, want 0", code)
	}
}

func TestWasmStructViewField(t *testing.T) {
	if code := runWasm(t, structViewFieldProgram); code != 0 {
		t.Errorf("wasm struct view field: exit = %d, want 0", code)
	}
}
