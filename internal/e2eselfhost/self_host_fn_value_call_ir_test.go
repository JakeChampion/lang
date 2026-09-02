package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fnValueCallCases pin calls made THROUGH a function value that no local slot
// describes — a struct field, an array element, a tuple element (#5986,
// docs/TYPED-IR-REWRITE.md §"Calls through a fn value that no slot carries").
//
// The zero-argument rows landed first: 17 of the 32 subtests they made up then
// failed on the sources they went in against. The
// self-host checker had no arm for a call whose callee is a value expression,
// so `fs[0]()` and `t.0()` typed unknown and the register backends read an f64
// result out of an integer register (exit 255 against an oracle of 45); and
// every one of these sites emitted the arity-keyed `op_call_indirect`, so wasm
// typed the funcref `(result i32)` and rejected the module outright.
//
// The _local rows bind the call's result to a declared local first, which is a
// control for the REGISTER half only: the declaration supplies the width there,
// but it says nothing about the funcref type, so those rows were failing on
// wasm too.
//
// The _i32 rows and the zero-argument fnlocal_annotated_* rows are the true
// controls — passing on both legs before and after. The all-i32 signatures
// decline the funcref tag on purpose (`$fn<N>` already describes them), which
// is what holds the emitted bytes identical for every program that was already
// correct.
//
// The fnfield_arg_* rows are the WITH-ARGUMENT half for a struct field, which a
// `fn_param_types` sidecar on StructFieldDecl carries. One declared signature
// drives both the width each argument is lowered at and the funcref type the
// call names; split across two derivations they disagree, and a disagreement is
// a module the validator rejects.
//
// The fntuple_arg_* rows are the same half for a TUPLE ELEMENT, which the slot
// carries as its declared tuple SPELLING (`tuple_type`) rather than a second
// list of tags: the element tags are the coarse "clo"/"fn" dispatch markers by
// design, and a signature cannot be recovered from those.
//
// The fnarray_arg_* rows are the same half for an ARRAY ELEMENT. An array is
// the one shape whose declared spelling does NOT survive — parse_type_name
// coarsens `((f64) => f64)[]` to the flat "fn[]" tag — so the element signature
// is rebuilt from the binding's own two sidecars and carried on the slot, and
// travels with a whole-array alias rebind.
//
// `mk()()` still emits an arity-keyed funcref. That residual and its cause are
// recorded in docs/TYPED-IR-REWRITE.md.
var fnValueCallCases = []struct {
	name string
	src  string
}{
	// A closure-valued STRUCT FIELD. Typed by the checker already; the funcref
	// signature was the missing half, so this failed on wasm only.
	{"fnfield_f64", `struct H { f: () => f64 }
function mkval(): f64 { return 4.5; }
function main(): i32 {
    var h: H = H { f: mkval };
    return (h.f() * 10.0) as i32;
}`},
	{"fnfield_f64_local", `struct H { f: () => f64 }
function mkval(): f64 { return 4.5; }
function main(): i32 {
    var h: H = H { f: mkval };
    var v: f64 = h.f();
    return (v * 10.0) as i32;
}`},
	{"fnfield_i64", `struct H { f: () => i64 }
function mkval(): i64 { return 7000000045i64; }
function main(): i32 {
    var h: H = H { f: mkval };
    return (h.f() % 1000i64) as i32;
}`},
	{"fnfield_i32", `struct H { f: () => i32 }
function mkval(): i32 { return 45; }
function main(): i32 {
    var h: H = H { f: mkval };
    return h.f();
}`},

	// An ARRAY ELEMENT. The parenthesised `(() => f64)[]` spelling recorded
	// nothing about its element, so the checker could not type the call at all.
	{"fnarray_f64", `function mkval(): f64 { return 4.5; }
function main(): i32 {
    var fs: (() => f64)[] = [mkval];
    return (fs[0]() * 10.0) as i32;
}`},
	{"fnarray_f64_local", `function mkval(): f64 { return 4.5; }
function main(): i32 {
    var fs: (() => f64)[] = [mkval];
    var v: f64 = fs[0]();
    return (v * 10.0) as i32;
}`},
	{"fnarray_i64", `function mkval(): i64 { return 7000000045i64; }
function main(): i32 {
    var fs: (() => i64)[] = [mkval];
    return (fs[0]() % 1000i64) as i32;
}`},
	{"fnarray_i32", `function mkval(): i32 { return 45; }
function main(): i32 {
    var fs: (() => i32)[] = [mkval];
    return fs[0]();
}`},
	{"fnarray_closure_f64", `function main(): i32 {
    var fs: (() => f64)[] = [function (): f64 { return 4.5; }];
    return (fs[0]() * 10.0) as i32;
}`},

	// A TUPLE ELEMENT. Its arrow spelling survives (#7961), so check_expr
	// already answered TypeFunc{ret: f64} — only check_call_expr's callee
	// dispatch was missing, and it bailed as a method call on a tuple.
	{"fntuple_f64", `function mkval(): f64 { return 4.5; }
function main(): i32 {
    var t: (() => f64, i32) = (mkval, 1);
    return (t.0() * 10.0) as i32;
}`},
	{"fntuple_f64_local", `function mkval(): f64 { return 4.5; }
function main(): i32 {
    var t: (() => f64, i32) = (mkval, 1);
    var v: f64 = t.0();
    return (v * 10.0) as i32;
}`},
	{"fntuple_i64", `function mkval(): i64 { return 7000000045i64; }
function main(): i32 {
    var t: (() => i64, i32) = (mkval, 1);
    return (t.0() % 1000i64) as i32;
}`},
	{"fntuple_i32", `function mkval(): i32 { return 45; }
function main(): i32 {
    var t: (() => i32, i32) = (mkval, 1);
    return t.0();
}`},

	// A fn-typed LOCAL declared with an arrow annotation. The declared return
	// reaches the binding through var_declared_type's fn_ret arm; without it
	// the local typed as the opaque `fn` tag with an unknown result.
	{"fnlocal_annotated_f64", `function mkval(): f64 { return 4.5; }
function main(): i32 {
    var f: () => f64 = mkval;
    return (f() * 10.0) as i32;
}`},
	{"fnlocal_annotated_i64", `function mkval(): i64 { return 7000000045i64; }
function main(): i32 {
    var f: () => i64 = mkval;
    return (f() % 1000i64) as i32;
}`},
	// A fn-typed local was the FIRST shape to carry a full funcref tag with
	// arguments, because a ParamDecl records parameter spellings. It failed on
	// both legs before, because the declared return never reached the binding.
	// A struct field carries its own spellings now, and a tuple slot keeps the
	// declared spelling its elements come from; an ARRAY element still has
	// nowhere to hold its parameters.
	{"fnlocal_annotated_arg_f64", `function scale(x: f64): f64 { return x * 10.0; }
function main(): i32 {
    var f: (f64) => f64 = scale;
    return f(4.5) as i32;
}`},

	// A call through a fn-typed struct FIELD carrying ARGUMENTS. The funcref
	// type must name every position, so these need the field's parameter
	// spellings, not just its return.
	{"fnfield_arg_f64", `struct H { f: (f64) => f64 }
function scale(x: f64): f64 { return x * 10.0; }
function main(): i32 {
    var h: H = H { f: scale };
    return h.f(4.5) as i32;
}`},
	{"fnfield_arg_mixed", `struct H { f: (i64, f64) => f64 }
function comb(a: i64, x: f64): f64 { return x * (a as f64); }
function main(): i32 {
    var h: H = H { f: comb };
    return h.f(10i64, 4.5) as i32;
}`},
	{"fnfield_arg_i64_ret", `struct H { f: (i64) => i64 }
function id64(x: i64): i64 { return x; }
function main(): i32 {
    var h: H = H { f: id64 };
    return h.f(45i64) as i32;
}`},
	{"fnfield_arg_i32_to_i64", `struct H { f: (i32) => i64 }
function widen(x: i32): i64 { return x as i64; }
function main(): i32 {
    var h: H = H { f: widen };
    return h.f(45) as i32;
}`},
	{"fnfield_arg_string", `struct H { f: (string) => i32 }
function slen(s: string): i32 { return s.len(); }
function main(): i32 {
    var h: H = H { f: slen };
    return h.f("x") + 44;
}`},
	{"fnfield_arg_i32", `struct H { f: (i32) => i32 }
function add5(x: i32): i32 { return x + 5; }
function main(): i32 {
    var h: H = H { f: add5 };
    return h.f(40);
}`},

	// A call through a fn-typed TUPLE ELEMENT carrying ARGUMENTS. The element
	// tag is the coarse "clo", so the funcref type comes from the declared
	// tuple spelling the slot now keeps.
	{"fntuple_arg_f64", `function scale(x: f64): f64 { return x * 10.0; }
function main(): i32 {
    var t: ((f64) => f64, i32) = (scale, 1);
    return t.0(4.5) as i32;
}`},
	{"fntuple_arg_mixed", `function comb(a: i64, x: f64): f64 { return x * (a as f64); }
function main(): i32 {
    var t: ((i64, f64) => f64, i32) = (comb, 1);
    return t.0(10i64, 4.5) as i32;
}`},
	{"fntuple_arg_i64_ret", `function id64(x: i64): i64 { return x; }
function main(): i32 {
    var t: ((i64) => i64, i32) = (id64, 1);
    return t.0(45i64) as i32;
}`},
	{"fntuple_arg_i32", `function add5(x: i32): i32 { return x + 5; }
function main(): i32 {
    var t: ((i32) => i32, i32) = (add5, 1);
    return t.0(40);
}`},
	{"fntuple_arg_string", `function slen(s: string): i32 { return s.len(); }
function main(): i32 {
    var t: ((string) => i32, i32) = (slen, 1);
    return t.0("x") + 44;
}`},

	// A call through a fn-typed ARRAY ELEMENT carrying ARGUMENTS. The whole
	// annotation coarsens to the flat "fn[]" tag, so — unlike the tuple, whose
	// spelling survives — the element signature has to be rebuilt from the two
	// sidecars the binding records (`fn_ret` and the new `fn_param_types`) and
	// carried on the slot as its declared element spelling.
	{"fnarray_arg_f64", `function scale(x: f64): f64 { return x * 10.0; }
function main(): i32 {
    var fs: ((f64) => f64)[] = [scale];
    return fs[0](4.5) as i32;
}`},
	{"fnarray_arg_mixed", `function comb(a: i64, x: f64): f64 { return x * (a as f64); }
function main(): i32 {
    var fs: ((i64, f64) => f64)[] = [comb];
    return fs[0](10i64, 4.5) as i32;
}`},
	{"fnarray_arg_i64_ret", `function id64(x: i64): i64 { return x; }
function main(): i32 {
    var fs: ((i64) => i64)[] = [id64];
    return fs[0](45i64) as i32;
}`},
	{"fnarray_arg_f64_to_i32", `function pick(x: f64): i32 { return (x as i32) + 1; }
function main(): i32 {
    var fs: ((f64) => i32)[] = [pick];
    return fs[0](44.5);
}`},
	// The all-i32 control: `$fn<N>` already names this signature, so the tag
	// declines and the emitted bytes are the ones this shape had before.
	{"fnarray_arg_i32", `function add5(x: i32): i32 { return x + 5; }
function main(): i32 {
    var fs: ((i32) => i32)[] = [add5];
    return fs[0](40);
}`},
	{"fnarray_arg_string", `function slen(s: string): i32 { return s.len(); }
function main(): i32 {
    var fs: ((string) => i32)[] = [slen];
    return fs[0]("x") + 44;
}`},
	// The whole-array ALIAS: the element spelling has to travel with the
	// rebind, or `xs[0]` reaches the call with nothing to name its funcref.
	{"fnarray_arg_alias", `function scale(x: f64): f64 { return x * 10.0; }
function main(): i32 {
    var fs: ((f64) => f64)[] = [scale];
    var xs = fs;
    return xs[0](4.5) as i32;
}`},
	// A CLOSURE element (capturing lambda), which dispatches env-first: the
	// same declared signature must drive that arm's argument widths and its
	// funcref tag, with the leading 'w' for the env box.
	{"fnarray_arg_closure_f64", `function main(): i32 {
    var k: f64 = 10.0;
    var fs: ((f64) => f64)[] = [(x: f64) => x * k];
    return fs[0](4.5) as i32;
}`},
	// A fn-pointer ARRAY struct FIELD with arguments — the field's own two
	// sidecars, which the parser now records for an array-of-fn field too.
	{"fnarrayfield_arg_f64", `struct Reg { hs: ((f64) => f64)[] }
function scale(x: f64): f64 { return x * 10.0; }
function main(): i32 {
    var r: Reg = Reg { hs: [scale] };
    return r.hs[0](4.5) as i32;
}`},

	// A GENERIC struct's fn field. Annotation runs on the erased form, so a
	// `(T) => T` field stamps nothing for the clone to inherit and the checker
	// carries no answer here — the declared sidecars, substituted by the
	// monomorphiser, are the only source. Without the width half the `as i32`
	// lowered with no i32.wrap_i64 and the module was refused.
	{"fnfield_generic_wide", `struct Box[T] { f: (T) => T }
function id64(x: i64): i64 { return x; }
function main(): i32 {
    var b: Box[i64] = Box[i64] { f: id64 };
    return b.f(45i64) as i32;
}`},
	{"fnfield_generic_i32", `struct Box[T] { f: (T) => T }
function id32(x: i32): i32 { return x; }
function main(): i32 {
    var b: Box[i32] = Box[i32] { f: id32 };
    return b.f(45);
}`},
	{"fnfield_generic_wide_field_arg", `struct Box[T] { f: (T) => T, seed: T }
function id64(x: i64): i64 { return x; }
function main(): i32 {
    var b: Box[i64] = Box[i64] { f: id64, seed: 45i64 };
    return b.f(b.seed) as i32;
}`},
}

// runFnValueCallCase compiles one case for one target and compares its exit
// code against the interpreter, the semantic oracle.
func runFnValueCallX86_64(t *testing.T, gcc string, runner []string, fernBin, stdlibRoot, name, src string, want int) {
	t.Helper()
	proj := t.TempDir()
	mainPath := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
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
		t.Errorf("%s = %d, want %d (interp oracle) — a call through a fn value must carry the callee's result type", name, got, want)
	}
}

// TestSelfHostFnValueCallX86_64 is the register-backend leg: it catches the
// half that was a SILENT wrong answer, where an f64/i64 result was read out of
// an integer register because the checker never typed the call.
func TestSelfHostFnValueCallX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range fnValueCallCases {
		t.Run(tc.name, func(t *testing.T) {
			runFnValueCallX86_64(t, gcc, runner, fernBin, stdlibRoot, tc.name, tc.src, interpExit(t, interpBin, tc.src))
		})
	}
}

// TestSelfHostFnValueCallWasm is the leg that gates the funcref SIGNATURE: a
// wasm module whose `call_indirect` is typed by arity alone claims `(result
// i32)` for every function value, so a wide result is not a wrong answer there
// but a module the validator refuses to load.
func TestSelfHostFnValueCallWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping fn-value call wasm cases")
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

	for _, tc := range fnValueCallCases {
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
				t.Errorf("%s = %d, want %d (interp oracle) — the funcref type must name the callee's real signature", tc.name, got, want)
			}
		})
	}
}
