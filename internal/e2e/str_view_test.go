package e2e

import "testing"

// strViewProgram exercises the `str` borrowed-string view type end-to-end
// (#4813 / #4297 Option A, slice 1): an owned `string` borrows into a `str`
// var and a `str` param; a `str` flows into a `string` param (borrowed
// position); the string method surface dispatches on a view receiver
// (builtin .len(), std/string .trim()); .to_owned() materialises an owned
// copy assignable to a `string` sink; `str[]` arrays work element-wise.
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
