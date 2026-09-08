package e2e

import "testing"

// Pin independent results in every backend, not just agreement between engines
// that could all inherit an incorrect literal annotation from the checker.
const numericJoinSettlementProgram = `
struct Box { tag: i32 }
enum Tag { A, B }
function literal_join(tag: i32): boolean {
    var narrow: f32 = 16777216.0;
    var value = match (tag) { 0 => 16777216.0, _ => narrow };
    return value + 1.0 == value;
}
function tuple_join(tag: (i32, i32)): boolean {
    var narrow: f32 = 16777216.0;
    var value = match (tag) { (0, _) => (16777216.0, 3), _ => (narrow, 3) };
    return value.0 + 1.0 == value.0 && value.1 == 3;
}
function struct_join(tag: Box): boolean {
    var narrow: f32 = 16777216.0;
    var value = match (tag) { Box { tag: 0 } => (3, (16777216.0, 4)), _ => (3, (narrow, 4)) };
    return value.1.0 + 1.0 == value.1.0 && value.1.1 == 4;
}
function enum_join(tag: Tag): boolean {
    var narrow: f32 = 16777216.0;
    var value = match (tag) { A => { var marker = 3; (16777216.0, marker) }, B => (narrow, 3) };
    return value.0 + 1.0 == value.0 && value.1 == 3;
}
function if_join(tag: boolean): boolean {
    var narrow: f32 = 16777216.0;
    var value = if (tag) { var marker = 3; (16777216.0, marker) } else { (narrow, 3) };
    return value.0 + 1.0 == value.0 && value.1 == 3;
}
function main(): i32 {
    if (!literal_join(0) || !literal_join(1)) { return 1; }
    if (!tuple_join((0, 0)) || !tuple_join((1, 0))) { return 2; }
    if (!struct_join(Box { tag: 0 }) || !struct_join(Box { tag: 1 })) { return 3; }
    if (!enum_join(Tag.A) || !enum_join(Tag.B)) { return 4; }
    if (!if_join(true) || !if_join(false)) { return 5; }
    return 0;
}
`

func TestNumericJoinSettlementInterp(t *testing.T) {
	if got := runInterpExit(t, numericJoinSettlementProgram); got != 0 {
		t.Fatalf("interp result = %d, want 0", got)
	}
}

func TestNumericJoinSettlementX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, numericJoinSettlementProgram); got != 0 {
		t.Fatalf("x86-64 result = %d, want 0", got)
	}
}

func TestNumericJoinSettlementArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, numericJoinSettlementProgram); got != 0 {
		t.Fatalf("ARM64 result = %d, want 0", got)
	}
}

func TestNumericJoinSettlementWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, numericJoinSettlementProgram); got != 0 {
		t.Fatalf("Wasm result = %d, want 0", got)
	}
}
