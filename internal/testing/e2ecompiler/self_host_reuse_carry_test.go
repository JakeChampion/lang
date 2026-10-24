package e2ecompiler

import "testing"

const reuseCarryProgram = `struct Tab { a: i32[], b: i32[], c: i32[] }
enum Kind { Pip, Idle, Bad(i32) }
@noinline function note(k: Kind): i32 {
    match (k) { Pip => { return 1; }, Idle => { return 2; }, Bad(n) => { return n; } }
    return 0;
}
function took(t: Tab, at: i32, i: i32): (Tab, boolean, Kind) {
    t = Tab { ...t, a: t.a.with(at, i), c: t.c.with(at, i + 1) };
    let kind: Kind = Idle;
    if (i % 3 == 0) { kind = Pip; } else if (i % 3 == 1) { kind = Bad(i); }
    let keep: boolean = i % 2 == 0;
    return (Tab { ...t, b: t.b.with(at, i) }, keep, kind);
}
@noinline function weigh(t: Tab, k: boolean): i32 {
    if (k) { return t.a[0] + t.b[0] + t.c[0]; }
    return t.b[0];
}
@noinline function rounds(): i32 {
    let before: i64 = __heap_alloc_count();
    let t: Tab = Tab { a: [0, 0], b: [0, 0], c: [0, 0] };
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        let took: (Tab, boolean, Kind) = took(t, 0, i);
        t = took.0;
        total = total + note(took.2) + weigh(t, took.1);
        i = i + 1;
    }
    return total * 1000 + ((__heap_alloc_count() - before) as i32);
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    print_int(rounds()); print("");
    return 0;
}
`

var reuseCarryProduced = []string{"took", "note", "weigh", "rounds"}

func TestSelfHostReuseCarry(t *testing.T) {
	runSemanticProgram(t, "reusecarry", reuseCarryProgram, reuseCarryProduced,
		semInlineWants("11617037\n"), "note", "weigh")
}
