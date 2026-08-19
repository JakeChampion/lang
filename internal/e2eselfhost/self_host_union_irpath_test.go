package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unionIRPathCases are UNION-type (`type Node = A | B`) programs whose value/
// statement `match` binds a variant payload (#3179). A union variant is a
// pre-existing struct with its REAL fields and NO synthetic `__ev` payload
// field, so irlower's match-arm bind discriminates it from a true enum variant
// (`enum E { V(T) }`, whose desugaring HAS `__ev`): for the union member it
// binds the WHOLE scrutinee box pointer typed with the variant's struct name
// (no offset-8 payload read), so a later field read (`Num(x) => x.value`)
// resolves. This mirrors the legacy AST emitter's union-member split
// (asm.fern:3685, `is_enum_variant` false -> bind the box itself).
//
// Before the fix every one of these bailed the whole module
// (the `__ev` read found no field, never typed the bound slot, and a later
// `x.value` bailed at irlower expr_struct_type == ""). This gate pins them at
// "ir" so a regression off the IR path — or a silent failure to flip — shows
// up. Pairs with the differential `union-*` cases in TestSelfHostAsmIRPath,
// which prove the chosen route produces the right exit code.
//
// The probe reuses asm_pathprobe_run (parser.module_with_builtins ->
// lift_lambdas -> asm_ir.all_eligible, the exact production decision) and
// prints "ir"/"ast" without emitting assembly.
var unionIRPathCases = []struct {
	name string
	src  string
}{
	{"union-eval", `struct Num { value: i32 } struct Add { left: i32, right: i32 } type Node = Num | Add; function eval(n: Node): i32 { match (n) { Num(x) => { return x.value; }, Add(a) => { return a.left + a.right; } } return 0; } function main(): i32 { return eval(Num { value: 7 }) * 100 + eval(Add { left: 3, right: 9 }); }`},
	{"union-multifield", `struct Pt { x: i32, y: i32 } struct Pt3 { x: i32, y: i32, z: i32 } type V = Pt | Pt3; function sum(v: V): i32 { match (v) { Pt(p) => { return p.x + p.y; }, Pt3(q) => { return q.x + q.y + q.z; } } return 0; } function main(): i32 { return sum(Pt { x: 3, y: 4 }) * 100 + sum(Pt3 { x: 1, y: 2, z: 3 }); }`},
	{"union-field-in-expr", `struct VInt { v: i32 } struct VStr { s: string } type Val = VInt | VStr; function size(x: Val): i32 { match (x) { VInt(i) => { return i.v * 2; }, VStr(s) => { return s.s.len() + 1; } } return -1; } function main(): i32 { return size(VInt { v: 20 }) + size(VStr { s: "abc" }); }`},
	{"union-nested-match", `struct Lit { n: i32 } struct Bin { l: i32, r: i32 } type Expr2 = Lit | Bin; function ev(e: Expr2): i32 { match (e) { Lit(x) => { return x.n; }, Bin(b) => { var t = 0; match (b.l > b.r) { _ => { t = b.l + b.r; } } return t; } } return 0; } function main(): i32 { return ev(Lit { n: 5 }) + ev(Bin { l: 10, r: 20 }); }`},
	{"union-method-on-field", `struct Box1 { v: i32 } struct Box2 { v: i32 } type B = Box1 | Box2; function (a: Box1) g(): i32 { return a.v + 1; } function (b: Box2) g(): i32 { return b.v + 100; } function pick(x: B): i32 { match (x) { Box1(p) => { return p.g(); }, Box2(q) => { return q.g(); } } return 0; } function main(): i32 { return pick(Box1 { v: 5 }) + pick(Box2 { v: 5 }); }`},
	{"union-wildcard-bind", `struct On { } struct Off { } type Sw = On | Off; function f(s: Sw): i32 { match (s) { On(_) => { return 1; }, Off(_) => { return 0; } } return 9; } function main(): i32 { return f(On { }) * 10 + f(Off { }); }`},
}

// TestSelfHostUnionIRPathX86_64 asserts each union-variant payload-bind program
// routes through the stack-IR path ("ir"), not the legacy AST emitter — the
// observable evidence the #3179 IR-coverage gap is closed.
func TestSelfHostUnionIRPathX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_pathprobe_run.fern")
	if err != nil {
		t.Fatalf("read asm_pathprobe_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_pathprobe_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_pathprobe_run.fern: %v", err)
	}
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range unionIRPathCases {
		t.Run(tc.name, func(t *testing.T) {
			out := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if out != "ir" {
				t.Errorf("%s routed through %q path, want \"ir\"", tc.name, out)
			}
		})
	}
}
