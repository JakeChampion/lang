package e2e

import "testing"

// strViewProgram exercises the `str` borrowed-string view type end-to-end
// (#4813 / #4297 Option A, slice 1): an owned `string` borrows into a `str`
// var and a `str` param; a `str` flows into a `string` param (borrowed
// position); the string method surface dispatches on a view receiver
// (builtin .len(), std/string .trim() — which returns `str` since the P1
// producer flip); .to_owned() materialises an owned copy assignable to a
// `string` sink; `str[]` arrays work element-wise; views compare by
// CONTENTS against strings, literals, and other views (IsStringCmp — a
// pointer compare here was the P1 near-miss), concat into fresh owned
// strings, index as byte reads, and re-slice into sub-views; slicing an
// OWNED string also yields a view (the P2 producer flip), which
// materialises into an owning sink via `+ ""`.
// StrType is erased to StringType at the LowerWith choke point
// (ir/erase_str.go), so a correct run proves the erasure feeds every
// backend a plain string program. Exits 0 on success, a distinct code per
// failed step.
const strViewProgram = `import "std/string";

function view_len(v: str): i32 {
    return v.len();
}

function owned_len(o: string): i32 {
    return o.len();
}

function main(): i32 {
    var s: string = "  hey  ";
    var v: str = s;
    if (view_len(s) != 7) { return 1; }
    if (view_len(v) != 7) { return 2; }
    if (owned_len(v) != 7) { return 3; }
    var t: str = v.trim();
    if (t.len() != 3) { return 4; }
    var o: string = t.to_owned();
    if (o.len() != 3) { return 5; }
    if (o != "hey") { return 6; }
    var vs: str[] = ["ab", "cde"];
    if (vs[0].len() + vs[1].len() != 5) { return 7; }
    if (t != "hey") { return 8; }
    if ("hey" != t) { return 9; }
    var w: str = t;
    if (w != t) { return 10; }
    if (t + "!" != "hey!") { return 11; }
    if (t[0] != 104) { return 12; }
    var sub: str = t[1:3];
    if (sub != "ey") { return 13; }
    var half: str = s[2:5];
    if (half != "hey") { return 14; }
    var oh: string = s[2:5] + "";
    if (oh != "hey") { return 15; }
    return 0;
}
`

func TestInterpStrView(t *testing.T) {
	if code := runInterpExit(t, strViewProgram); code != 0 {
		t.Errorf("interp str view: exit = %d, want 0", code)
	}
}

func TestX86_64StrView(t *testing.T) {
	if _, code := compileAndRunX86_64(t, strViewProgram); code != 0 {
		t.Errorf("x86-64 str view: exit = %d, want 0", code)
	}
}
