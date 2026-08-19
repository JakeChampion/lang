package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// closureLocalOptRetCases pin the Option/Result return recovery for a closure
// LOCAL (#5646 option 3).
//
// `closure_opt_rets` was seeded only from fn-typed PARAMS (`ParamDecl.fn_ret`),
// so a match on a call through a closure local had no way to name its scrutinee
// type. The `alias` case below is what that closes.
//
// The load-bearing detail is that the lift runs BEFORE lowering: by the time
// `var f = <lambda>` reaches irlower its init is a `__mkclo$<cloname>` marker
// call, whose callee ident is not itself a module function — `<cloname>`, after
// the 8-char prefix, is. Reading the callee name directly recovers nothing and
// the whole recovery goes inert.
//
// The guards are part of the pin, not decoration: each is a neighbouring shape
// that already worked, so a regression in the shared helper is attributed to
// this change rather than to the new case.
//
// Every `want` stays in [0, 126) — the wasm leg exits through WASI, which
// rejects anything above that.
//
// wasmOpen marks a case that lowers on x86-64 but still bails on the wasm IR
// path, which has its own deferral gate on top of the shared eligibility. Such
// a case is asserted on x86-64 and skipped with its reason on wasm — not
// dropped, so the day wasm closes the gap the skip is what tells us.
var closureLocalOptRetCases = []struct {
	name     string
	src      string
	want     int
	wasmOpen string
}{
	// The case this closes: an alias of a closure local propagates the
	// recorded return type.
	{"alias", `
function main(): i32 {
    var f: () => Option[i32] = function (): Option[i32] { return Some(7); };
    var g: () => Option[i32] = f;
    match (g()) { Some(v) => { return v; }, None => { return 1; } }
}
`, 7, ""},
	// Guard: the fn-PARAM path, which the param seeding already covered.
	{"fn-param", `
function call(f: () => Option[i32]): i32 {
    match (f()) { Some(v) => { return v; }, None => { return 1; } }
}
function main(): i32 { return call(function (): Option[i32] { return Some(6); }); }
`, 6, ""},
	// Guard: a local bound to a NAMED function, which try_opt_type already
	// resolved via opt_ret_type.
	{"named-fn-init", `
function g(): Option[i32] { return Some(5); }
function main(): i32 {
    var f: () => Option[i32] = g;
    match (f()) { Some(v) => { return v; }, None => { return 1; } }
}
`, 5, "pre-existing wasm-only bail, unrelated to this change: clo_init is false for a named-fn ident init (g is not a closure local), so the closure-local recovery never runs on this shape — confirmed identical on a driver built from the pre-change tree"},
	// Guard: splitting the call out of the scrutinee. This is also the
	// documented rewrite for the shape that is still open (see the file
	// footer), so it must keep working.
	{"split-call", `
function main(): i32 {
    var f: () => Option[i32] = function (): Option[i32] { return Some(5); };
    var o: Option[i32] = f();
    match (o) { Some(v) => { return v; }, None => { return 1; } }
}
`, 5, ""},
	// Guard: a closure array with a non-Option return — unaffected by any of
	// the Option recovery.
	{"non-option-closure-array", `
function main(): i32 {
    var fs: (() => i32)[] = [function (): i32 { return 9; }];
    return fs[0]();
}
`, 9, ""},
	// Guard: a closure local returning a non-Option composite.
	{"string-closure-local", `
function main(): i32 {
    var f: () => string = function (): string { return "abcde"; };
    return f().len();
}
`, 5, ""},
}

// STILL OPEN, and deliberately not asserted here: the DIRECT shape
//
//	var f: () => Option[i32] = <lambda>; match (f()) { … }
//
// It does not bail on the scrutinee type — instrumenting that bail site shows it
// is never reached. It bails in emit_module_ir_gated's const_func check with
//
//	FERN_STRICT_IR: main (function value main$clo not defined)
//
// i.e. the lowered IR references a hoisted closure `<fn>$clo` that the lift did
// not append to the module. That is a lift/registration gap, not an Option
// recovery one, which is why a scrutinee-side fix leaves it untouched while
// closing `alias` above. `split-call` is the working rewrite.

// TestSelfHostClosureLocalOptRetIRX86_64 asserts each shape lowers on the IR
// path — proven by running under FERN_STRICT_IR, where a bail is exit 3 — and
// still produces the interpreter's answer.
func TestSelfHostClosureLocalOptRetIRX86_64(t *testing.T) {
	gcc, runner, driverBin := strictIRDriver(t)
	dir := filepath.Dir(driverBin)

	for _, tc := range closureLocalOptRetCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, stderr, code := runDriver(t, runner, driverBin, []byte(tc.src), true)
			if code != 0 || len(asm) == 0 {
				t.Fatalf("%s did not lower on the IR path (exit %d):\n%s", tc.name, code, stderr)
			}
			progBin := buildBin(t, gcc, dir, "clooptret_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostClosureLocalOptRetIRWasm runs the same cases through the wasm IR
// backend, which shares the resolver.
func TestSelfHostClosureLocalOptRetIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host closure-local opt-ret wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range closureLocalOptRetCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wasmOpen != "" {
				t.Skipf("open on the wasm IR path: %s", tc.wasmOpen)
			}
			wat, stderr, code := runDriver(t, runner, driverBin, []byte(tc.src), true, "-ir")
			if code != 0 || len(wat) == 0 {
				t.Fatalf("%s did not lower on the wasm IR path (exit %d):\n%s", tc.name, code, stderr)
			}
			watFile := filepath.Join(dir, "closure_local_optret_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := run.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("closure-local opt-ret wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
