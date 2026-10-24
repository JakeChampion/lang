package e2e

import "testing"

const bindThenVarSrc = `enum E { Tup(i32[]), Num(i32) }
function eval(e: E): i32 {
    match (e) {
        Tup(t) => {
            let i: i32 = 0;
            let s: i32 = 0;
            while (i < t.len()) { s = s + t[i]; i = i + 1; }
            return s;
        },
        Num(n) => {
            let t: string = "n" + "x";
            return t.len() + n;
        },
    }
}
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    if (eval(Tup(xs)) != 6) { return 1; }
    if (eval(Num(5)) != 7) { return 2; }
    if (eval(Tup(xs)) != 6) { return 3; }
    return 0;
}`

const varThenBindSrc = `enum E { Tup(i32[]), Num(i32) }
function eval(e: E): i32 {
    match (e) {
        Num(n) => {
            let t: string = "n" + "x";
            return t.len() + n;
        },
        Tup(t) => {
            let i: i32 = 0;
            let s: i32 = 0;
            while (i < t.len()) { s = s + t[i]; i = i + 1; }
            return s;
        },
    }
}
function main(): i32 {
    if (eval(Num(5)) != 7) { return 1; }
    let xs: i32[] = [2, 4, 6];
    if (eval(Tup(xs)) != 12) { return 2; }
    if (eval(Num(1)) != 3) { return 3; }
    return 0;
}`

const bindThenLoopVarSrc = `enum E { Tup(i32[]), Num(i32) }
function eval(e: E, q: string): string {
    match (e) {
        Tup(t) => { if (t.len() > 99) { return "big"; } },
        Num(n) => {},
    }
    let t: string = q + "!";
    let out: string = "";
    let i: i32 = 0;
    while (i < 3) { out = out + t; i = i + 1; }
    return out;
}
function main(): i32 {
    let xs: i32[] = [1, 2];
    let r1: string = eval(Tup(xs), "ab");
    if (r1 != "ab!ab!ab!") { return 1; }
    let r2: string = eval(Num(4), "z");
    if (r2 != "z!z!z!") { return 2; }
    return 0;
}`

const ifLetCrossSrc = `function pick(o: Option[i32[]], t: string): i32 {
    let n: i32 = 0;
    if let Some(t2) = o {
        n = t2.len();
    }
    return n * 100 + t.len();
}
function samename(o: Option[i32[]]): i32 {
    let t: string = "ab" + "c";
    let n: i32 = 0;
    if let Some(t) = o {
        n = t.len();
    }
    return n * 100 + t.len();
}
function main(): i32 {
    let xs: i32[] = [7, 8];
    if (pick(Some(xs), "qq") != 202) { return 1; }
    if (samename(Some(xs)) != 203) { return 2; }
    if (samename(None) != 3) { return 3; }
    return 0;
}`

func TestArm64BindingSlotCrossShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"bind-then-var", bindThenVarSrc},
		{"var-then-bind", varThenBindSrc},
		{"bind-then-loop-var", bindThenLoopVarSrc},
		{"if-let-cross", ifLetCrossSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, tc.src); code != 0 {
				t.Errorf("%s: got %d, want 0 (cross-shape slot split — #4510)", tc.name, code)
			}
		})
	}
}

func TestX86_64BindingSlotCrossShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"bind-then-var", bindThenVarSrc},
		{"var-then-bind", varThenBindSrc},
		{"bind-then-loop-var", bindThenLoopVarSrc},
		{"if-let-cross", ifLetCrossSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, tc.src); code != 0 {
				t.Errorf("%s: got %d, want 0", tc.name, code)
			}
		})
	}
}

func TestWASMBindingSlotCrossShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"bind-then-var", bindThenVarSrc},
		{"var-then-bind", varThenBindSrc},
		{"bind-then-loop-var", bindThenLoopVarSrc},
		{"if-let-cross", ifLetCrossSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != 0 {
				t.Errorf("%s: got %d, want 0 (two-word wasm strings share the #4510 shape)", tc.name, got)
			}
		})
	}
}
