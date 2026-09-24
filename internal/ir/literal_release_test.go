package ir

import "testing"

func countStrDecs(prog *Program, name string) int {
	n := 0
	for _, fn := range prog.Funcs {
		if fn.Name != name {
			continue
		}
		for _, op := range fn.Ops {
			if op.Kind == OpCallDirect && op.Str == "__fern_str_dec" {
				n++
			}
		}
	}
	return n
}

// A helper returning a literal, once inlined, leaves its result slot holding
// only that literal, and the release after its use goes. A slot that can also
// hold a string built at run time keeps its release.
func TestLiteralOnlySlotLosesItsRelease(t *testing.T) {
	src := `function pairs(): string { return "00010203040506070809"; }
@noinline function g(s: string, i: i32): i32 { return s.len() + i; }
@noinline function lit(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        t = t + g(pairs(), i);
        i = i + 1;
    }
    return t;
}
@noinline function built(n: i32, k: string): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var s: string = pairs();
        if (i > 1) { s = k + "x"; }
        t = t + g(s, i);
        i = i + 1;
    }
    return t;
}
function main(): i32 { return lit(3) + built(3, "a"); }`
	prog := lowerSourceWith(t, src, 8)
	OptimizeProgram(prog, 8)
	if n := countStrDecs(prog, "lit"); n != 0 {
		t.Errorf("lit releases its literal %d times, want none", n)
	}
	if n := countStrDecs(prog, "built"); n == 0 {
		t.Errorf("built lost the release of a slot that can hold a built string")
	}
}
