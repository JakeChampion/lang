package e2e

import "testing"

const foreachPatternProg = `
import "core/map";

function pairs(): (i32, string)[] {
    return [(1, "a"), (2, "bb"), (3, "ccc")];
}

function arrayForms(): i32 {
    let sum: i32 = 0;
    let xs: (i32, i32)[] = [(1, 2), (3, 4)];
    for (a, b) in xs { sum = sum + a * b; }          // 2 + 12 = 14
    if (sum != 14) { return 0 - 1; }

    let wide: (i32, i32, i32)[] = [(1, 2, 3), (4, 5, 6)];
    for (p, _, r) in wide { sum = sum + p + r; }     // 4 + 10 = 14
    if (sum != 28) { return 0 - 2; }

    let deep: ((i32, i32), string)[] = [((2, 3), "xy")];
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
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    m = m.insert(2, 20);
    let sum: i32 = 0;
    for (k, v) in m { sum = sum + k + v; }           // 33
    if (sum != 33) { return 0 - 5; }

    // Insertion order, and a break that leaves the cursor walk early.
    let first: i32 = 0;
    for (k, v) in m {
        first = k;
        break;
    }
    if (first != 1) { return 0 - 6; }

    let t: Map[i32, (i32, i32)] = map_new(4);
    t = t.insert(7, (10, 100));
    for (k, (lo, hi)) in t { sum = sum + k + lo + hi; }  // 117
    if (sum != 150) { return 0 - 7; }
    return sum;
}

function main(): i32 {
    let a: i32 = arrayForms();
    if (a != 46) { return 0 - a; }
    let m: i32 = mapForms();
    if (m != 150) { return 0 - m; }
    return 42;
}
`

const foreachPatternNestingProg = `
function apply(f: () => i32): i32 {
    return f();
}

function main(): i32 {
    let xs: (i32, i32)[] = [(1, 2), (3, 4)];

    let viaLambda = apply((): i32 => {
        let s = 0;
        for (a, b) in xs { s = s + a * b; }
        return s;
    });
    if (viaLambda != 14) { return 1; }

    let viaBlock: i32 = {
        let s = 0;
        for (a, b) in xs { s = s + a + b; }
        s
    };
    if (viaBlock != 10) { return 2; }

    let s2 = 0;
    if (xs.len() == 2) for (a, b) in xs { s2 = s2 + a; }
    if (s2 != 4) { return 3; }

    let opt: i32 = 1;
    let s3 = 0;
    match (opt) {
        1 => { for (a, b) in xs { s3 = s3 + b; } },
        _ => {},
    }
    if (s3 != 6) { return 4; }

    let s4 = 0;
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

const foreachPatternIterandProg = `
import "core/map";

function twice[T](x: T): (T, T) {
    return (x, x);
}

function pairsOf(n: i32, f: (i32) => i32): (i32, i32)[] {
    let out: (i32, i32)[] = [];
    let i = 0;
    while (i < n) {
        out = out.append((i, f(i)));
        i = i + 1;
    }
    return out;
}

function makeMap(n: i32): Map[i32, i32] {
    let m: Map[i32, i32] = map_new(8);
    let i = 0;
    while (i < n) {
        m = m.insert(i, i * 3);
        i = i + 1;
    }
    return m;
}

function main(): i32 {
    let bump = 5;
    let total = 0;

    let (l, r) = twice(7);
    total = total + l + r;

    for (a, b) in pairsOf(3, (x: i32) => x + bump) {
        total = total + a + b;
    }

    for (k, v) in makeMap(3) {
        total = total + k + v;
    }

    if (total != 47) { return total; }
    return 42;
}
`

func TestForEachPatternIterandInterp(t *testing.T) {
	if got := runInterpExit(t, foreachPatternIterandProg); got != 42 {
		t.Fatalf("interp got %d, want 42 (any other value is the running total)", got)
	}
}

func TestForEachPatternIterandX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, foreachPatternIterandProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (any other value is the running total)", got)
	}
}

func TestForEachPatternIterandArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, foreachPatternIterandProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (any other value is the running total)", got)
	}
}

func TestForEachPatternIterandWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, foreachPatternIterandProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (any other value is the running total)", got)
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
