package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A value binding SHADOWS a module-level function of the same name. That
// held for a plain module function but NOT for a generic one (#6302): the
// checker keys `Info.GenericFuncs` by bare name, so `function apply(v: i32,
// id: (i32) => i32)` calling `id(v)` resolved to a module-level `id[T]`.
// The visible symptom was E040 naming a type parameter the call site never
// mentions; behind it, destination refinement stamped TypeArgs onto the
// call, monomorph rewrote the callee to `id__i32`, and every backend then
// emitted a DIRECT call to the identity generic instead of an indirect call
// through the closure — so once the diagnostic was silenced the program
// still returned the wrong value. Both halves are covered here: the program
// has to check AND produce 39.
//
// The three shadowing shapes exercise the two arms of the resolution order
// the fix mirrors (scope, then captureChain):
//   - `apply`'s fn-typed PARAMETER (the issue's repro) — 7 |> inc = 8
//   - main's local `var id` — 10 |> twice = 20
//   - `via_capture` reading that local as a CAPTURE — 4 |> twice = 8
//
// `plain` keeps calling the real generic in the same module, so the fix
// cannot be "stop treating `id` as generic everywhere" — 3 |> id = 3.
const shadowedGenericProg = `function id[T](x: T): T { return x; }
function inc(x: i32): i32 { return x + 1; }
function twice(x: i32): i32 { return x * 2; }
function apply(v: i32, id: (i32) => i32): i32 { return id(v); }
function plain(v: i32): i32 { return id(v); }
function main(): i32 {
    var id: (i32) => i32 = twice;
    function via_capture(v: i32): i32 { return id(v); }
    return apply(7, inc) + id(10) + plain(3) + via_capture(4);
}
`

// The same shadowing rule for a `use` clause's source call (#6302).
// inferUseParam read `Info.FuncSigs` before the scope, so `use x <-
// withRes();` under a shadowing `withRes` bound `x` from the unrelated
// MODULE `withRes` — `i32` rather than the `string` the binding's own
// callback takes — while the call still dispatched to the binding.
//
// The shadow is a `var` rather than a fn-typed parameter because a
// parameter would have to be written `((string) => i32) => i32`, and the
// self-host parser does not accept a parenthesised fn type nested in a
// parameter position (a separate, pre-existing gap). The `var` form
// needs no such annotation and both frontends parse it, so the two
// legs below run the same source.
const shadowedUseProg = `function withRes(cb: (i32) => i32): i32 { return cb(4); }
function taker(f: (string) => i32): i32 { return f("hi"); }
function g(): i32 {
    var withRes = taker;
    use x <- withRes();
    if (x == "hi") { return 21; }
    return 0;
}
function main(): i32 { return g(); }
`

// The self-host legs run both programs through one driver build.
var shadowedCases = []struct {
	name string
	src  string
	want int
}{
	{"shadowed_generic", shadowedGenericProg, 39},
	{"shadowed_use", shadowedUseProg, 21},
}

// TestNativeShadowedGenericCall pins the shadowing on the native backends
// (interp / x86-64 / wasm). Before #6302 every leg here failed at the
// checker; with the diagnostic alone fixed they would have returned 21
// (`id` resolving to the identity generic at all three shadowed sites).
func TestNativeShadowedGenericCall(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(p, []byte(shadowedGenericProg), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, code := runFixtureInterp(t, p, ""); code != 39 {
		t.Errorf("shadowed-generic interp = %d, want 39", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 39 {
		t.Errorf("shadowed-generic x86-64 = %d, want 39", code)
	}
	if code := runWasm(t, shadowedGenericProg); code != 39 {
		t.Errorf("shadowed-generic wasm = %d, want 39", code)
	}
}

// TestNativeShadowedUseClauseCallee is the `use`-clause half on the
// native backends. Before the fix this failed at the checker, with an
// E038 comparing the module callee's signature against the binding's.
func TestNativeShadowedUseClauseCallee(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(p, []byte(shadowedUseProg), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, code := runFixtureInterp(t, p, ""); code != 21 {
		t.Errorf("shadowed-use interp = %d, want 21", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 21 {
		t.Errorf("shadowed-use x86-64 = %d, want 21", code)
	}
	if code := runWasm(t, shadowedUseProg); code != 21 {
		t.Errorf("shadowed-use wasm = %d, want 21", code)
	}
}

// TestSelfHostShadowedGenericCallIRX86_64 runs the same program through the
// self-hosted x86-64 compiler. Its frontend resolves the shadowing on its
// own — this leg is the cross-check that the two frontends now AGREE, since
// the native one was the divergent side. Compiling at all is the IR-path
// assertion: the AST emitters are deleted, so every backend routes
// IR-or-error and a module outside the subset is a diagnostic, not a
// fall-through.
func TestSelfHostShadowedGenericCallIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range shadowedCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s self-host x86-64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostShadowedGenericCallIRWasm is the wasm IR leg of the same
// cross-check.
func TestSelfHostShadowedGenericCallIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host shadowed-generic wasm IR e2e")
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

	for _, tc := range shadowedCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runc := exec.Command("wasmtime", "run", watFile)
			_ = runc.Run()
			if runc.ProcessState == nil || !runc.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s:\n%s", tc.name, wat)
			}
			if code := runc.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s wasm IR = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
