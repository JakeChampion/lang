package e2e

import "testing"

const strViewProgram = `import "std/string";

function view_len(v: str): i32 {
    return v.len();
}

function owned_len(o: string): i32 {
    return o.len();
}

function main(): i32 {
    let s: string = "  hey  ";
    let v: str = s;
    if (view_len(s) != 7) { return 1; }
    if (view_len(v) != 7) { return 2; }
    if (owned_len(v) != 7) { return 3; }
    let t: str = v.trim();
    if (t.len() != 3) { return 4; }
    let o: string = t.to_owned();
    if (o.len() != 3) { return 5; }
    if (o != "hey") { return 6; }
    let vs: str[] = ["ab", "cde"];
    if (vs[0].len() + vs[1].len() != 5) { return 7; }
    if (t != "hey") { return 8; }
    if ("hey" != t) { return 9; }
    let w: str = t;
    if (w != t) { return 10; }
    if (t + "!" != "hey!") { return 11; }
    if (t[0] != 104) { return 12; }
    let sub: str = slice_unchecked(t, 1, 3);
    if (sub != "ey") { return 13; }
    let half: str = slice_unchecked(s, 2, 5);
    if (half != "hey") { return 14; }
    let oh: string = slice_unchecked(s, 2, 5) + "";
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
