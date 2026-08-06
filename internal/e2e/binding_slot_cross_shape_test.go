package e2e

import "testing"

// These pin #4510: a two-word slot (arm64/wasm strings under the two-word
// ABI, inline dyn on wasm32) and a one-word match binding sharing a NAME
// used to make (*builder).bindingSlotScoped allocate a fresh slot and PERMANENTLY
// remap b.locals[name]. Everything lowered after the arm then resolved the
// name to the wrong-shaped slot — a later-lowered `var t: string` stored its
// two words through the binding's one-word slot (operand imbalance /
// garbage reads), the exit dec sweep swept the wrong slot, and the entry
// zero-init covered a slot nothing read. Observed in the wild as the
// self-host interp's ExprTuple loop trapping its own arm64 bounds check
// (exit 134) once a sibling arm gained `var t: string` (#4497). The fix
// makes the cross-shape remap SCOPED: bindingSlotScoped restores the
// shadowed mapping right after the arm/Then body is lowered.
//
// x86-64 uses one-word strings, so these shapes were never miscompiled
// there — it runs as the parity leg. arm64 (TwoWordOverride) is the
// discriminating backend; wasm (ptrW 4, two-word strings) mirrors it.

// bindThenVar puts the ONE-WORD binding arm FIRST, so pre-fix the remap is
// live when the SECOND arm's two-word `var t: string` lowers — its store
// went through the binding's one-word slot.
const bindThenVarSrc = `enum E { Tup(i32[]), Num(i32) }
function eval(e: E): i32 {
    match (e) {
        Tup(t) => {
            var i: i32 = 0;
            var s: i32 = 0;
            while (i < t.len()) { s = s + t[i]; i = i + 1; }
            return s;
        },
        Num(n) => {
            var t: string = "n" + "x";
            return t.len() + n;
        },
    }
}
function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    if (eval(Tup(xs)) != 6) { return 1; }
    if (eval(Num(5)) != 7) { return 2; }
    if (eval(Tup(xs)) != 6) { return 3; }
    return 0;
}`

// varThenBind reverses the order: the two-word var's slot is entry-mapped,
// so the binding arm hits the cross-shape fresh-slot path directly; the
// var arm lowers AFTER the binding arm's (pre-fix permanent) remap.
const varThenBindSrc = `enum E { Tup(i32[]), Num(i32) }
function eval(e: E): i32 {
    match (e) {
        Num(n) => {
            var t: string = "n" + "x";
            return t.len() + n;
        },
        Tup(t) => {
            var i: i32 = 0;
            var s: i32 = 0;
            while (i < t.len()) { s = s + t[i]; i = i + 1; }
            return s;
        },
    }
}
function main(): i32 {
    if (eval(Num(5)) != 7) { return 1; }
    var xs: i32[] = [2, 4, 6];
    if (eval(Tup(xs)) != 12) { return 2; }
    if (eval(Num(1)) != 3) { return 3; }
    return 0;
}`

// bindThenLoopVar is the discriminator: the one-word binding arm lowers
// FIRST, and a sibling-scoped two-word `var t: string` PLUS a string loop
// lower AFTER it. Pre-fix, the permanent remap made the var store its
// two-word concat through the binding's one-word slot and every loop
// iteration's loads desynchronised the operand stack — SEGFAULT on arm64
// (reproduced: exit 139 pre-fix, 0 with the scoped restore).
const bindThenLoopVarSrc = `enum E { Tup(i32[]), Num(i32) }
function eval(e: E, q: string): string {
    match (e) {
        Tup(t) => { if (t.len() > 99) { return "big"; } },
        Num(n) => {},
    }
    var t: string = q + "!";
    var out: string = "";
    var i: i32 = 0;
    while (i < 3) { out = out + t; i = i + 1; }
    return out;
}
function main(): i32 {
    var xs: i32[] = [1, 2];
    var r1: string = eval(Tup(xs), "ab");
    if (r1 != "ab!ab!ab!") { return 1; }
    var r2: string = eval(Num(4), "z");
    if (r2 != "z!z!z!") { return 2; }
    return 0;
}`

// ifLetCross exercises the if-let scoped restore: the binding `t` (one
// word) collides with a two-word string var in the enclosing function that
// is READ AFTER the if-let — pre-fix the permanent remap made that read
// resolve the binding's slot.
const ifLetCrossSrc = `function pick(o: Option[i32[]], t: string): i32 {
    var n: i32 = 0;
    if let Some(t2) = o {
        n = t2.len();
    }
    return n * 100 + t.len();
}
function samename(o: Option[i32[]]): i32 {
    var t: string = "ab" + "c";
    var n: i32 = 0;
    if let Some(t) = o {
        n = t.len();
    }
    return n * 100 + t.len();
}
function main(): i32 {
    var xs: i32[] = [7, 8];
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
