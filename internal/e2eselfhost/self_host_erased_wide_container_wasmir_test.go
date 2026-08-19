package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostErasedWideContainerWasm pins the CONTAINER half of the erased-wide
// close. A single-type-arg builtin container return of an erased type var —
// `some1[T](x: T): Option[T]` or `dup[T](x: T): T[]` — passing a 64-bit / f64
// value now LOWERS on the wasm IR path instead of being refused.
//
// Unlike the pass-through (#5586) and tuple (#5593) slices, a container's box
// LAYOUT shifts with payload width (an Option is 8B/payload-@4 for i32 but
// 16B/payload-@8 for i64/f64; an array stride is 4 vs 8), so the erased box can't
// be read back unambiguously by widening the shared fn. Instead the parser's
// targeted promotion (has_bare_scalar_param + feeds_wide_container) promotes such
// a fn's erased type var to BOUNDED, so monomorphize_module CLONES it per concrete
// instantiation — `some1__i64(x: i64): Option[i64]` with a concrete 16B box. After
// cloning no call passes a wide value through a bare-typevar param, so
// module_erased_wide clears and the wasm IR driver's mono_ok rescue admits the
// module (wasm_ir_run judges eligibility on the SAME monomorphised module it
// emits). Cases assert the module reached the IR path (no `$__lit0` AST-fallback
// locals) and computes the right value under wasmtime; values cross-checked
// against the native interpreter.
//
// The `result2-*` cases cover the GENUINELY two-typevar Result[T, E] shape
// (`okg[T, E](x: T): Result[T, E]`) that the single-var promotion (clause c,
// all_tp_count==1) deliberately left open: promoting only T strands E erased on
// the Err arm. The new clause (c′) (result_two_bare_vars) promotes BOTH vars — T
// binds from the bare-scalar arg, the return-only E from the call-site annotation
// via infer_inst_ret — so the clone is fully concrete and lowers where the erased
// two-var Result deferred. This closes the last per-function IR-subset remnant.
func TestSelfHostErasedWideContainerWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-wide container wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	const some = `function some1[T](x: T): Option[T] { return Some(x); }`
	const dup = `function dup[T](x: T): T[] { return [x, x]; }`
	const okr = `function okr[T](x: T): Result[T, string] { return Ok(x); }`
	const errg = `function errg[E](e: E): Result[i32, E] { return Err(e); }`
	const okg = `function okg[T, E](x: T): Result[T, E] { return Ok(x); }`
	cases := []struct {
		name string
		src  string
		want int
	}{
		// Option[i64]: the clone some1__i64 returns the concrete 16B Option box, so
		// the wide value round-trips. 5e9/1e9 = 5, +37 = 42.
		{"opt-i64",
			some + ` function main(): i32 { var a: Option[i64] = some1[i64](5000000000 as i64); var r: i64 = 0; match (a) { Some(v) => { r = v; }, None => {} } return (r / 1000000000) as i32 + 37; }`,
			42},
		// Option[f64]: same, via the concrete f64 payload box. 2.5*2 = 5, +37 = 42.
		{"opt-f64",
			some + ` function main(): i32 { var a: Option[f64] = some1[f64](2.5); var r: f64 = 0.0; match (a) { Some(v) => { r = v; }, None => {} } return (r * 2.0) as i32 + 37; }`,
			42},
		// A bare wide LITERAL binds the clone's T from magnitude (mono_infer's
		// literal_is_i64), so `some1(5000000000)` clones some1__i64 — not the
		// truncating some1__i32. Guards the instantiation-key inference.
		{"opt-bare-literal",
			some + ` function main(): i32 { var a: Option[i64] = some1(5000000000); var r: i64 = 0; match (a) { Some(v) => { r = v; }, None => {} } return (r / 1000000000) as i32 + 37; }`,
			42},
		// Array T[] of a wide element: the clone dup__i64 returns a concrete i64[]
		// (8-byte stride). both elements read back full-width. 5+5+... check len+val.
		{"arr-i64",
			dup + ` function main(): i32 { var a: i64[] = dup[i64](5000000000 as i64); if (a.len() == 2 && a[0] == 5000000000 as i64 && a[1] == 5000000000 as i64) { return 42; } return 38; }`,
			42},
		// A string (pointer) Option still round-trips: some1__string keeps the 8B
		// box. Guards that promotion does not regress a non-wide container caller.
		{"opt-string",
			some + ` function main(): i32 { var a: Option[string] = some1[string]("hello"); var r: i32 = 0; match (a) { Some(v) => { r = v.len(); }, None => {} } return r + 37; }`,
			42},
		// An i32 Option is the pre-existing narrow shape — promotion clones
		// some1__i32 but the box stays 8B, value unchanged. 5 + 37 = 42.
		{"opt-i32",
			some + ` function main(): i32 { var a: Option[i32] = some1[i32](5); var r: i32 = 0; match (a) { Some(v) => { r = v; }, None => {} } return r + 37; }`,
			42},
		// Single-typevar Result with a concrete Err arm: the clone
		// okr__i64 returns Result[i64, string] (concrete Err), so the wide Ok
		// round-trips. 5e9/1e9 = 5, +37 = 42. The all_tp_count==1 guard makes
		// this sound (no sibling typevar left erased).
		{"result-ok-wide",
			okr + ` function main(): i32 { var a: Result[i64, string] = okr[i64](5000000000 as i64); var r: i64 = 0; match (a) { Ok(v) => { r = v; }, Err(e) => {} } return (r / 1000000000) as i32 + 37; }`,
			42},
		// Single-typevar Result with a wide Err arm (`Result[i32, E]`): errg__i64
		// returns Result[i32, i64], the wide Err value round-trips. 5+37 = 42.
		{"result-err-wide",
			errg + ` function main(): i32 { var a: Result[i32, i64] = errg[i64](5000000000 as i64); var r: i64 = 0; match (a) { Ok(v) => {}, Err(e) => { r = e; } } return (r / 1000000000) as i32 + 37; }`,
			42},
		// Genuinely two-typevar Result[T, E] (okg[T, E](x: T): Result[T, E]): the
		// clause-(c) all_tp_count==1 guard leaves this open, so the NEW clause (c′)
		// (result_two_bare_vars) promotes BOTH vars — T from the arg, E from the
		// call-site annotation via infer_inst_ret. The concrete clone
		// okg__i64_string returns Result[i64, string], so the wide Ok round-trips on
		// the wasm IR path where the erased two-var Result was refused. 5e9/1e9 =
		// 5, +37 = 42.
		{"result2-ok-wide-explicit",
			okg + ` function main(): i32 { var a: Result[i64, string] = okg[i64, string](5000000000 as i64); var r: i64 = 0; match (a) { Ok(v) => { r = v; }, Err(e) => {} } return (r / 1000000000) as i32 + 37; }`,
			42},
		// The SAME shape with the type args INFERRED — okg(x) with no `[i64, string]`.
		// E (return-only) can only come from the `Result[i64, string]` annotation, so
		// this exercises the infer_inst_ret expected-return binding that makes the
		// two-var promotion resolvable at all.
		{"result2-ok-wide-inferred",
			okg + ` function main(): i32 { var a: Result[i64, string] = okg(5000000000 as i64); var r: i64 = 0; match (a) { Ok(v) => { r = v; }, Err(e) => {} } return (r / 1000000000) as i32 + 37; }`,
			42},
		// Wide Err arm (Result[i32, i64]): okg__i32_i64 keeps the Err var concrete
		// too, so an i64 Err value round-trips full-width. 5 + 37 = 42.
		{"result2-err-wide",
			okg + ` function main(): i32 { var a: Result[i32, i64] = okg[i32, i64](5); var r: i32 = 0; match (a) { Ok(v) => { r = v; }, Err(e) => {} } return r + 37; }`,
			42},
		// f64 Ok arm (Result[f64, string]): the concrete clone's f64 payload box
		// round-trips the float. 2.5*2 = 5, +37 = 42.
		{"result2-f64",
			okg + ` function main(): i32 { var a: Result[f64, string] = okg[f64, string](2.5); var r: f64 = 0.0; match (a) { Ok(v) => { r = v; }, Err(e) => {} } return (r * 2.0) as i32 + 37; }`,
			42},
		// Narrow (Result[i32, string]) — regression guard that promoting the two-var
		// shape leaves a non-wide caller's value unchanged. 5 + 37 = 42.
		{"result2-narrow",
			okg + ` function main(): i32 { var a: Result[i32, string] = okg[i32, string](5); var r: i32 = 0; match (a) { Ok(v) => { r = v; }, Err(e) => {} } return r + 37; }`,
			42},
		// Multi-type-param guard: a two-typevar generic whose `init: A` + `A[]`
		// return matches clause (c)'s inner shape (bare scalar param feeding a
		// container of it) must NOT be promoted — promoting only A would leave the
		// sibling T erased in the clone (`mk2__i32` with `t: T`), a malformed clone
		// (this is the std/array `scan[T, A]` shape that crashed CI). The
		// all_tp_count==1 guard keeps it fully erased, so it lowers on the IR path
		// (i32, not wide) and computes correctly. 5+5 = 10, +32 = 42.
		{"multi-tparam-unpromoted",
			`function mk2[T, A](t: T, init: A): A[] { var out: A[] = [init]; return out.append(init); } function main(): i32 { var r: i32[] = mk2[i32, i32](7, 5); return r[0] + r[1] + 32; }`,
			42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			if strings.Contains(string(wat), "$__lit0") {
				t.Errorf("%s did not lower through the IR (found $__lit0)", tc.name)
			}
			watFile := filepath.Join(dir, "cont_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s (an invalid module fails to load)", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("erased-wide container wasm IR %s = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
