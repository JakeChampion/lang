package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// annotateConsumerCases pin the consumer half of the typed-IR migration
// (#5986, docs/TYPED-IR-REWRITE.md): predicates and binding marks in
// irlower.fern that re-derived an expression's type structurally while the
// checker's stamped carrier tag on the same node went unread. Each case was
// first demonstrated as a live defect against the interp oracle — an IR bail
// under FERN_STRICT_IR or a silent wrong answer — and compiles clean with the
// carrier read wired in. The _control siblings perturb each shape onto the
// structural walk (an annotated local, a named type) and pin that the walk
// still answers first where it resolves.
//
// The shapes cover: the expr_is_bool/str/u32/u64 Call/FieldAccess/Index/Unary
// arms; expr_struct_type's Index and operator-overload arms; expr_map_kind /
// expr_map_type_tag map-array elements; arr_tag_of's sliced base;
// expr_opt_elem_tag's method callee; expr_tuple_elem_tag's FieldAccess
// fallback; lower_i64's Call load site paired with infer_expr_width;
// method_recv_tyname's associated bare-type receiver; the mark_tuple/map
// binding transfers; the lift-time detector_expr_type / cap_type_expr /
// cap_type_in_stmts family; and ty survival through the monomorphiser's
// array/map method folds.
var annotateConsumerCases = []struct {
	name string
	src  string
}{
	// gap g00 — was: wrong-output (bool-arms)
	{"bool_call_closure_local", `import "core/cmp";
function main(): i32 {
    var flip: boolean = false;
    var g = function (): boolean { return true; };
    if (flip) { g = function (): boolean { return false; }; }
    var n: i32 = g().to_string().len();
    if (n == 4) { return 42; }
    return n;
}`},
	{"bool_call_closure_local_control", `import "core/cmp";
function main(): i32 {
    var flip: boolean = false;
    var g = function (): boolean { return true; };
    if (flip) { g = function (): boolean { return false; }; }
    var b: boolean = g();
    var n: i32 = b.to_string().len();
    if (n == 4) { return 42; }
    return n;
}`},
	// gap g01 — was: wrong-output (bool-arms)
	{"bool_tuple_elem_to_string", `import "core/cmp";
function main(): i32 {
    var t: (boolean, i32) = (true, 1);
    var n: i32 = t.0.to_string().len();
    if (n == 4) { return 42; }
    return n;
}`},
	{"bool_tuple_elem_to_string_control", `import "core/cmp";
function main(): i32 {
    var t: (boolean, i32) = (true, 1);
    var b: boolean = t.0;
    var n: i32 = b.to_string().len();
    if (n == 4) { return 42; }
    return n;
}`},
	// gap g02 — was: wrong-output (bool-arms)
	{"bool_neg_overload", `import "core/cmp";
struct V { b: boolean }
function (a: V) neg(): boolean { return !a.b; }
function main(): i32 {
    var v: V = V { b: false };
    var r = -v;
    var n: i32 = r.to_string().len();
    if (n == 4) { return 42; }
    return n;
}`},
	{"bool_neg_overload_control", `import "core/cmp";
struct V { b: boolean }
function (a: V) neg(): boolean { return !a.b; }
function main(): i32 {
    var v: V = V { b: false };
    var r: boolean = -v;
    var n: i32 = r.to_string().len();
    if (n == 4) { return 42; }
    return n;
}`},
	// gap g03 — was: wrong-output (str-u32-arms)
	{"str_tuple_elem_of_call", `enum Color { Red }

function f(): (string, Color) {
    return ("hello", Red);
}

function main(): i32 {
    var n: i32 = f().0.len();
    if (n == 5) { return 42; }
    return 1;
}`},
	{"str_tuple_elem_of_call_control", `function f(): (string, i32) {
    return ("hello", 7);
}

function main(): i32 {
    var n: i32 = f().0.len();
    if (n == 5) { return 42; }
    return 1;
}`},
	// gap g05 — was: wrong-output (str-u32-arms)
	{"str_neg_overload", `struct W { s: string }

function (a: W) neg(): string {
    return a.s;
}

function main(): i32 {
    var w: W = W { s: "hello" };
    var n: i32 = (-w).len();
    if (n == 5) { return 42; }
    return 1;
}`},
	{"str_neg_overload_control", `struct W { s: string }

function (a: W) neg(): string {
    return a.s;
}

function main(): i32 {
    var w: W = W { s: "hello" };
    var n: i32 = w.neg().len();
    if (n == 5) { return 42; }
    return 1;
}`},
	// gap g06 — was: wrong-output (str-u32-arms)
	{"u32_index_of_call_result", `function mk(): u32[] {
    return [2147484527u32];
}

function main(): i32 {
    if ((mk()[0] >> 1u32) == 1073742263u32) { return 42; }
    return 1;
}`},
	{"u32_index_of_call_result_control", `function mk(): u32[] {
    return [2147484527u32];
}

function main(): i32 {
    var xs: u32[] = mk();
    if ((xs[0] >> 1u32) == 1073742263u32) { return 42; }
    return 1;
}`},
	// gap g07 — was: wrong-output (str-u32-arms)
	{"u32_neg_overload_compare", `struct U { v: u32 }

function (a: U) neg(): u32 {
    return a.v;
}

function main(): i32 {
    var u1: U = U { v: 2147483649u32 };
    var u2: U = U { v: 5u32 };
    if ((-u1) > (-u2)) { return 42; }
    return 1;
}`},
	{"u32_neg_overload_compare_control", `struct U { v: u32 }

function (a: U) neg(): u32 {
    return a.v;
}

function main(): i32 {
    var u1: U = U { v: 2147483649u32 };
    var u2: U = U { v: 5u32 };
    if (u1.neg() > u2.neg()) { return 42; }
    return 1;
}`},
	// gap g08 — was: bail (struct-composite)
	{"struct_elem_of_sliced_array", `struct P { x: i32 }
function (p: P) get(): i32 { return p.x; }
function main(): i32 {
    var ps: P[] = [P { x: 40 }, P { x: 2 }];
    return ps[1:][0].get();
}`},
	{"struct_elem_of_sliced_array_control", `struct P { x: i32 }
function (p: P) get(): i32 { return p.x; }
function main(): i32 {
    var ps: P[] = [P { x: 40 }, P { x: 2 }];
    var q: P = ps[1:][0];
    return q.get();
}`},
	// gap g09 — was: bail (arr-opt-tuple)
	{"opt_scrutinee_sliced_base", `function main(): i32 {
    var xs: Option[i32][] = [Some(1), None, Some(3)];
    match (xs[1:][1]) {
        Some(v) => { return v + 39; },
        None => { return 9; }
    }
}`},
	{"opt_scrutinee_sliced_base_control", `function main(): i32 {
    var xs: Option[i32][] = [Some(1), None, Some(3)];
    match (xs[2]) {
        Some(v) => { return v + 39; },
        None => { return 9; }
    }
}`},
	// gap g10 — was: wrong-output (struct-composite)
	{"map_array_elem_tuple", `import "core/map";
function main(): i32 {
    var m1: Map[i32, i32] = Map { 1: 10 };
    var ms: Map[i32, i32][] = [m1];
    var t = (ms[0], 7);
    return t.0.len() + t.1;
}`},
	{"map_array_elem_tuple_control", `import "core/map";
function main(): i32 {
    var m1: Map[i32, i32] = Map { 1: 10 };
    var ms: Map[i32, i32][] = [m1];
    var t = (m1, 7);
    return t.0.len() + t.1;
}`},
	// gap g11 — was: bail (arr-opt-tuple)
	{"opt_tuple_elem_method_call", `struct S { n: i32 }
function (a: S) find(k: i32): Option[i32] { return Some(a.n + k); }
function main(): i32 {
    var st: S = S { n: 3 };
    var t = (st.find(4), 35);
    match (t.0) {
        Some(v) => { return v + t.1; },
        None => { return 9; }
    }
}`},
	{"opt_tuple_elem_method_call_control", `struct S { n: i32 }
function find_free(a: S, k: i32): Option[i32] { return Some(a.n + k); }
function main(): i32 {
    var st: S = S { n: 3 };
    var t = (find_free(st, 4), 35);
    match (t.0) {
        Some(v) => { return v + t.1; },
        None => { return 9; }
    }
}`},
	// gap g12 — was: bail (arr-opt-tuple)
	{"tuple_elem_nested_fieldaccess", `function main(): i32 {
    var big: i64 = 4294967338i64;
    var x: i64 = ((big, 2i64), 3).0.0;
    if (x == big) { return 42; }
    return 7;
}`},
	{"tuple_elem_nested_fieldaccess_control", `function main(): i32 {
    var big: i64 = 4294967338i64;
    var t = ((big, 2i64), 3);
    var x: i64 = t.0.0;
    if (x == big) { return 42; }
    return 7;
}`},
	// gap g13 — was: bail (struct-composite)
	{"struct_overload_iife_operand", `struct V { x: i32 }
function (a: V) add(b: V): V { return V { x: a.x + b.x }; }
enum E { A(V), B(V) }
function main(): i32 {
    var w: V = V { x: 10 };
    var e: E = A(V { x: 32 });
    var r = match (e) { A(q) => { q + w }, B(p) => { p + w } };
    return r.x;
}`},
	{"struct_overload_iife_operand_control", `struct V { x: i32 }
function (a: V) add(b: V): V { return V { x: a.x + b.x }; }
enum E { A(V), B(V) }
function main(): i32 {
    var w: V = V { x: 10 };
    var e: E = A(V { x: 32 });
    var r = match (e) { A(q) => { q.add(w) }, B(p) => { p.add(w) } };
    return r.x;
}`},
	// gap g14 — was: wrong-output (lift-detector)
	{"lift_detector_unannotated_local", `struct R { hs: ((i32) => i32)[] }
function mk(n: i32): R {
    return R { hs: [function (x: i32): i32 { return x + n; }] };
}
function pick(): (i32) => i32 {
    var r = mk(41);
    return r.hs[0];
}
function main(): i32 {
    var g = pick();
    return g(1);
}`},
	{"lift_detector_unannotated_local_control", `struct R { hs: ((i32) => i32)[] }
function mk(n: i32): R {
    return R { hs: [function (x: i32): i32 { return x + n; }] };
}
function pick(): (i32) => i32 {
    var r: R = mk(41);
    return r.hs[0];
}
function main(): i32 {
    var g = pick();
    return g(1);
}`},
	// gap g15 — was: wrong-output (lift-detector)
	{"lift_detector_index_elem", `struct H { f: (i32) => i32 }
function mkh(n: i32): H {
    return H { f: function (x: i32): i32 { return x + n; } };
}
function load(): H[] {
    return [mkh(41)];
}
function pick(): (i32) => i32 {
    var kvs = load();
    return kvs[0].f;
}
function main(): i32 {
    var g = pick();
    return g(1);
}`},
	{"lift_detector_index_elem_control", `struct H { f: (i32) => i32 }
function mkh(n: i32): H {
    return H { f: function (x: i32): i32 { return x + n; } };
}
function load(): H[] {
    return [mkh(41)];
}
function pick(): (i32) => i32 {
    var kvs: H[] = load();
    return kvs[0].f;
}
function main(): i32 {
    var g = pick();
    return g(1);
}`},
	// gap g16 — was: wrong-output (lift-detector)
	{"lift_detector_call_chain", `struct R { hs: ((i32) => i32)[] }
function mk(n: i32): R {
    return R { hs: [function (x: i32): i32 { return x + n; }] };
}
function pick(): (i32) => i32 {
    return mk(41).hs[0];
}
function main(): i32 {
    var g = pick();
    return g(1);
}`},
	{"lift_detector_call_chain_control", `struct R { hs: ((i32) => i32)[] }
function mk(n: i32): R {
    return R { hs: [function (x: i32): i32 { return x + n; }] };
}
function pick(): (i32) => i32 {
    var r: R = mk(41);
    return r.hs[0];
}
function main(): i32 {
    var g = pick();
    return g(1);
}`},
	// gap g18 — was: bail (lift-captype)
	{"lift_cap_module_const", `const LIMIT: i32 = 41;

function f(): i32 {
    var k = LIMIT;
    var g = () => k + 1;
    return g();
}

function main(): i32 {
    return f();
}`},
	{"lift_cap_module_const_control", `const LIMIT: i32 = 41;

function f(): i32 {
    var k: i32 = LIMIT;
    var g = () => k + 1;
    return g();
}

function main(): i32 {
    return f();
}`},
	// gap g19 — was: bail (lift-captype)
	{"lift_cap_wide_arith", `function f(a: i64, b: i64): i64 {
    var k = a * b;
    var g = () => k + 1i64;
    return g();
}

function main(): i32 {
    return f(5i64, 8i64) as i32;
}`},
	{"lift_cap_wide_arith_control", `function f(a: i64, b: i64): i64 {
    var k: i64 = a * b;
    var g = () => k + 1i64;
    return g();
}

function main(): i32 {
    return f(5i64, 8i64) as i32;
}`},
	// gap g20 — was: bail (lift-captype)
	{"lift_cap_builtin_call", `function f(s: string): i32 {
    var n = s.len();
    var g = () => n + 1;
    return g();
}

function main(): i32 {
    return f("fortyone!");
}`},
	{"lift_cap_builtin_call_control", `function f(s: string): i32 {
    var n: i32 = s.len();
    var g = () => n + 1;
    return g();
}

function main(): i32 {
    return f("fortyone!");
}`},
	// gap g21 — was: bail (mono-survival)
	{"mono_arrm_fold_tuple_ty", `// Gap 21: generic ARRAY-method fold (parser.fern:9034) rebuilds xs.stats()
// as __arrm_stats__u32(xs) with ty:"" — dropping the checker's "(f64, i32)"
// stamp that expr_tuple_elem_tag's ExprCall arm (irlower.fern:2394) needs.
function (xs: T[]) stats(): (f64, i32) {
    return (xs.len() as f64 + 0.25, 7);
}

function main(): i32 {
    var xs: u32[] = [5u32, 6u32];
    return (xs.stats().0 * 4.0) as i32;
}`},
	{"mono_arrm_fold_tuple_ty_control", `// Control for gap 21: same tuple-returning call read at .0, but through a
// FREE function — no __arrm_ fold, so the mono fallthrough (parser.fern:9096)
// carries ty: c.ty and the stamp reaches irlower.
function stats(xs: u32[]): (f64, i32) {
    return (xs.len() as f64 + 0.25, 7);
}

function main(): i32 {
    var xs: u32[] = [5u32, 6u32];
    return (stats(xs).0 * 4.0) as i32;
}`},
	// gap g22 — was: bail (mono-survival)
	{"mono_mapm_fold_tuple_ty", `// Gap 22: generic MAP-method fold (parser.fern:9060) rebuilds m.stats()
// as __mapm_stats__string;i32(m) with ty:"" — dropping the checker's
// "(f64, i32)" stamp that expr_tuple_elem_tag's ExprCall arm
// (irlower.fern:2394) needs to width the .0 read.
import "core/map";

function (m: Map[K, V]) stats(): (f64, i32) {
    return (2.25, 7);
}

function main(): i32 {
    var m: Map[string, i32] = Map { "a": 1, "b": 2 };
    return (m.stats().0 * 4.0) as i32;
}`},
	{"mono_mapm_fold_tuple_ty_control", `// Control for gap 22: same tuple-returning call read at .0, but through a
// FREE function — no __mapm_ fold, so the mono fallthrough (parser.fern:9096)
// carries ty: c.ty and the stamp reaches irlower.
import "core/map";

function stats(m: Map[string, i32]): (f64, i32) {
    return (2.25, 7);
}

function main(): i32 {
    var m: Map[string, i32] = Map { "a": 1, "b": 2 };
    return (stats(m).0 * 4.0) as i32;
}`},
	// gap g24 — was: wrong-output (registry-siblings)
	{"assoc_method_i64_width", `trait HasStart { function start(): i64; }

struct Counter { n: i32 }

impl HasStart for Counter {
    function start(): i64 { return 5000000000i64; }
}

function main(): i32 {
    var t: i64 = Counter.start();
    if (t == 5000000000i64) { return 42; }
    return 1;
}`},
	{"assoc_method_i64_width_control", `function start_c(): i64 { return 5000000000i64; }

function main(): i32 {
    var t: i64 = start_c();
    if (t == 5000000000i64) { return 42; }
    return 1;
}`},
	// gap g27 — was: wrong-output (registry-siblings)
	{"tuple_bind_from_call_array", `function mk(): (i32, string)[] {
    return [(1, "abcd")];
}

function main(): i32 {
    var t = mk()[0];
    return t.1.len() + 38;
}`},
	{"tuple_bind_from_call_array_control", `function mk(): (i32, string)[] {
    return [(1, "abcd")];
}

function main(): i32 {
    return mk()[0].1.len() + 38;
}`},
}

// TestSelfHostAnnotateConsumersX86_64 runs the cases on the self-host x86-64
// backend. fern.fern is the driver because it runs checker.annotate_module; a
// driver that skips annotation leaves every ty empty and exercises only the
// structural walk.
func TestSelfHostAnnotateConsumersX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range annotateConsumerCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asmPath := filepath.Join(proj, "out.s")
			if out, cerr := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", mainPath, stdlibRoot, "-o", asmPath).CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			binPath := filepath.Join(proj, "out.bin")
			if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
				t.Fatalf("link: %v (%s)", lerr, out)
			}
			rcmd := runX86_64Bin(runner, binPath)
			_ = rcmd.Run()
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — the consumer must read the carrier where the walk cannot answer", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostAnnotateConsumersWasm is the wasm leg — it carries the cases
// whose pre-fix failure was wasm-shaped (an invalid module, a signed
// shift/divide on an unsigned value that x86-64's zero-extension hides).
func TestSelfHostAnnotateConsumersWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping annotate-consumer wasm cases")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range annotateConsumerCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			var stderr strings.Builder
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", mainPath, stdlibRoot, "-o", outWat)
			cmd.Stderr = &stderr
			if cerr := cmd.Run(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, stderr.String())
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — the consumer must read the carrier where the walk cannot answer", tc.name, got, want)
			}
		})
	}
}
