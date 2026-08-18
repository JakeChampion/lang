package e2e

import "testing"

// arrayViewLendProgram exercises the `T[]` → `[T]` argument borrow (#6798)
// end-to-end: an owned array flows into a view PARAMETER without the caller
// spelling a range, at concrete and generic element types, from an array
// literal, from a struct method's argument list, and re-lent through a view
// parameter into another one. The explicit slice spellings — including the
// newly un-reserved full-range `xs[:]` — run alongside them, because the
// implicit form desugars to exactly `xs[:]` and the two must not diverge.
// Exits 0 on success, a distinct code per failed step.
const arrayViewLendProgram = `struct Sink { base: i32 }

function sum_u8(bs: [u8]): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < bs.len()) { t = t + (bs[i] as i32); i = i + 1; }
    return t;
}

function sum_i32(xs: [i32]): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { t = t + xs[i]; i = i + 1; }
    return t;
}

function count[T](xs: [T]): i32 {
    return xs.len();
}

function relend(bs: [u8]): i32 {
    return sum_u8(bs);
}

function tail_sum(bs: [u8]): i32 {
    return sum_u8(bs[1:]);
}

function (s: Sink) take(xs: [i32]): i32 {
    return s.base + sum_i32(xs);
}

function main(): i32 {
    var bytes: u8[] = [1, 2, 3];
    var ints: i32[] = [10, 20, 30];

    if (sum_u8(bytes) != 6) { return 1; }
    if (sum_i32(ints) != 60) { return 2; }

    if (count(bytes) != 3) { return 3; }
    if (count(ints) != 3) { return 4; }

    if (sum_u8([4, 5]) != 9) { return 5; }

    if (sum_u8(bytes[:]) != 6) { return 6; }
    if (sum_u8(bytes[1:]) != 5) { return 7; }
    if (sum_u8(bytes[:2]) != 3) { return 8; }

    if (relend(bytes) != 6) { return 9; }
    if (tail_sum(bytes) != 5) { return 10; }

    var s = Sink { base: 5 };
    if (s.take(ints) != 65) { return 11; }

    // Lending does not consume: the array is still owned by the caller
    // afterwards, and lends again.
    if (sum_u8(bytes) + bytes.len() != 9) { return 12; }

    return 0;
}
`

func TestInterpArrayViewLend(t *testing.T) {
	if code := runInterpExit(t, arrayViewLendProgram); code != 0 {
		t.Errorf("interp array view lend: exit = %d, want 0", code)
	}
}

func TestX86_64ArrayViewLend(t *testing.T) {
	if _, code := compileAndRunX86_64(t, arrayViewLendProgram); code != 0 {
		t.Errorf("x86-64 array view lend: exit = %d, want 0", code)
	}
}

func TestArm64ArrayViewLend(t *testing.T) {
	if _, code := compileAndRunArm64(t, arrayViewLendProgram); code != 0 {
		t.Errorf("arm64 array view lend: exit = %d, want 0", code)
	}
}

func TestWasmArrayViewLend(t *testing.T) {
	if code := runWasm(t, arrayViewLendProgram); code != 0 {
		t.Errorf("wasm array view lend: exit = %d, want 0", code)
	}
}
