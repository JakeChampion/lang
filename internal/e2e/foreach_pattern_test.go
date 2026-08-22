package e2e

// A `for` header takes a destructuring pattern (#6096), the same one
// `var (a, b) = e;` takes. Which loop it lowers to is chosen by the iterand's
// type — an array binds the pattern against each element, a Map against each
// entry — so both halves have to run, and the Map half is the control that must
// not regress.

import "testing"

// Every shape the shared pattern grammar can put in the header: a plain pair, a
// wider arity, a nested element, a `_` discard, an iterand that is a call rather
// than a name, and `continue` inside a labelled loop (which must still advance
// the index). The Map legs pin the entry cursor: insertion order, a nested
// value pattern, and a `break` that leaves the walk early.
const foreachPatternProg = `
import "core/map";

function pairs(): (i32, string)[] {
    return [(1, "a"), (2, "bb"), (3, "ccc")];
}

function arrayForms(): i32 {
    var sum: i32 = 0;
    var xs: (i32, i32)[] = [(1, 2), (3, 4)];
    for (a, b) in xs { sum = sum + a * b; }          // 2 + 12 = 14
    if (sum != 14) { return 0 - 1; }

    var wide: (i32, i32, i32)[] = [(1, 2, 3), (4, 5, 6)];
    for (p, _, r) in wide { sum = sum + p + r; }     // 4 + 10 = 14
    if (sum != 28) { return 0 - 2; }

    var deep: ((i32, i32), string)[] = [((2, 3), "xy")];
    for ((a, b), s) in deep { sum = sum + a * b + s.len(); }  // 6 + 2 = 8
    if (sum != 36) { return 0 - 3; }

    // The iterand is evaluated once, into a slot the loop reads.
    each: for (n, s) in pairs() {
        if (n == 2) { continue; }
        sum = sum + n * s.len();                     // 1 + 9 = 10
    }
    if (sum != 46) { return 0 - 4; }
    return sum;
}

function mapForms(): i32 {
    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    m = m.insert(2, 20);
    var sum: i32 = 0;
    for (k, v) in m { sum = sum + k + v; }           // 33
    if (sum != 33) { return 0 - 5; }

    // Insertion order, and a break that leaves the cursor walk early.
    var first: i32 = 0;
    for (k, v) in m {
        first = k;
        break;
    }
    if (first != 1) { return 0 - 6; }

    var t: Map[i32, (i32, i32)] = map_new(4);
    t = t.insert(7, (10, 100));
    for (k, (lo, hi)) in t { sum = sum + k + lo + hi; }  // 117
    if (sum != 150) { return 0 - 7; }
    return sum;
}

function main(): i32 {
    var a: i32 = arrayForms();
    if (a != 46) { return 0 - a; }
    var m: i32 = mapForms();
    if (m != 150) { return 0 - m; }
    return 42;
}
`

// The lowering is swapped in where a block's statements are checked, so every
// position a statement can sit in has to reach it: a lambda body, a
// block-expression, a braceless branch body (the parser wraps the loop in a
// Block so this one is a statement of something), a match arm, and one pattern
// loop inside another.
const foreachPatternNestingProg = `
function apply(f: () => i32): i32 {
    return f();
}

function main(): i32 {
    var xs: (i32, i32)[] = [(1, 2), (3, 4)];

    var viaLambda = apply((): i32 => {
        var s = 0;
        for (a, b) in xs { s = s + a * b; }
        return s;
    });
    if (viaLambda != 14) { return 1; }

    var viaBlock: i32 = {
        var s = 0;
        for (a, b) in xs { s = s + a + b; }
        s
    };
    if (viaBlock != 10) { return 2; }

    var s2 = 0;
    if (xs.len() == 2) for (a, b) in xs { s2 = s2 + a; }
    if (s2 != 4) { return 3; }

    var opt: i32 = 1;
    var s3 = 0;
    match (opt) {
        1 => { for (a, b) in xs { s3 = s3 + b; } },
        _ => {},
    }
    if (s3 != 6) { return 4; }

    var s4 = 0;
    for (a, b) in xs {
        for (c, d) in xs { s4 = s4 + a * c + b * d; }
    }
    if (s4 != 52) { return 5; }

    return 42;
}
`

func TestForEachPatternNestingInterp(t *testing.T) {
	if got := runInterpExit(t, foreachPatternNestingProg); got != 42 {
		t.Fatalf("interp got %d, want 42 (the code names the position that did not lower)", got)
	}
}

func TestForEachPatternNestingX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, foreachPatternNestingProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (the code names the position that did not lower)", got)
	}
}

func TestForEachPatternNestingArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, foreachPatternNestingProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (the code names the position that did not lower)", got)
	}
}

func TestForEachPatternNestingWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, foreachPatternNestingProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (the code names the position that did not lower)", got)
	}
}

func TestForEachPatternInterp(t *testing.T) {
	if got := runInterpExit(t, foreachPatternProg); got != 42 {
		t.Fatalf("interp got %d, want 42 (a negative code names the failing check)", got)
	}
}

func TestForEachPatternX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, foreachPatternProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (a negative code names the failing check)", got)
	}
}

func TestForEachPatternArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, foreachPatternProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (a negative code names the failing check)", got)
	}
}

func TestForEachPatternWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, foreachPatternProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (a negative code names the failing check)", got)
	}
}
