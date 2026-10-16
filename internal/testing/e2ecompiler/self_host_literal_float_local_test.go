package e2ecompiler

import "testing"

// Checking alone accepted several of these shapes while lowering retained
// integer or f64 locals at their old representation (#12395). Exercise both
// arms, including f32 and whole-composite demands, on every backend.
func TestSelfHostLiteralFloatLocals(t *testing.T) {
	const program = `function pick[T](a: T, b: T): T { return a; }
@noinline function exercise(c: boolean): i32 {
    let q = 2;
    let x = if (c) { q } else { 2.5 };
    if (c && x != 2.0 || !c && x != 2.5) { return 1; }
    let block = if (c) { ({ let v = 2; v }) } else { 2.5 };
    if (block != x) { return 2; }
    let generic = if (c) { pick(1, 2) } else { 2.5 };
    if (c && generic != 1.0 || !c && generic != 2.5) { return 3; }
    let y = 2.5;
    let z: f32 = y;
    if (z != 2.5f32) { return 4; }
    let tuple = (1, 2);
    let joined = if (c) { tuple } else { (3.5, 4) };
    if (c && joined.0 != 1.0 || !c && joined.0 != 3.5) { return 5; }
    if (c && joined.1 != 2 || !c && joined.1 != 4) { return 6; }
    let n = 2;
    let pair = if (c) { (n, 1) } else { (2.5, 2) };
    if (pair.0 != x) { return 7; }
    let value = 2;
    let option = if (c) { Some(value) } else { Some(2.5) };
    match (option) { Some(v) => { if (v != x) { return 8; } }, None => { return 9; } }
    let xs = [1, 2];
    let wide: u64[] = xs;
    if (wide[0] != 1u64 || wide[1] != 2u64) { return 10; }
    let base = 3;
    let prior = base + 1;
    let ratio = if (c) { base / 2 } else { 2.5 };
    let later: i64 = prior;
    if (c && ratio != 1.5 || !c && ratio != 2.5) { return 11; }
    if (later != 4i64) { return 12; }
    let options: (Option[i32], i32)[] = [if (c) { (None, 1) } else { (Some(2), 2) }];
    if (c && options[0].1 != 1 || !c && options[0].1 != 2) { return 13; }
    match (options[0].0) {
        Some(v) => { if (c || v != 2) { return 14; } },
        None => { if (!c) { return 15; } }
    }
    return 0;
}
function main(): i32 {
    let yes: i32 = exercise(true);
    if (yes != 0) { return yes; }
    let no: i32 = exercise(false);
    if (no != 0) { return no; }
    print("ok");
    return 0;
}
`
	runSemanticProgram(t, "literal-float-local", program, []string{"exercise"}, semInlineWants("ok\n"))
}
