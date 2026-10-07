package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// annotateF64Cases exercise the checker's annotation pass (#5531,
// docs/TYPED-IR-REWRITE.md): checker.annotate_module stamps ExprCall.ty with
// the inferred result type, and each program's float-ness flows through such a
// CALL. Getting the float verdict wrong mis-selects an integer op on the
// double's bits, so the exit codes below (an `as i32` of the float result) are
// a direct oracle on the typed path.
//
// Each case asserts `-decide` reports "ir" — the typed lowering accepts the
// whole program — so a regression that refuses one fails loudly. Oracle: the
// interp.
var annotateF64Cases = []struct {
	name string
	src  string
}{
	// f64-returning free function; result cast via `as i32`.
	{"free_ret", `function scale(x: f64): f64 { return x * 2.5; }
function main(): i32 { let a: f64 = scale(4.0); return a as i32; }`}, // 10
	// two f64 calls feeding float arithmetic (each must type f64 so `+` is fadd).
	{"call_plus_call", `function f(x: f64): f64 { return x + 1.5; }
function main(): i32 { return (f(2.0) + f(0.5)) as i32; }`}, // 5
	// nested f64 calls.
	{"nested", `function f(x: f64): f64 { return x + 1.5; }
function g(x: f64): f64 { return x * 3.0; }
function main(): i32 { return (f(g(2.0)) + g(f(1.0))) as i32; }`}, // 15
	// f64 call result multiplied by a float literal (chained float op depends on
	// the call typing as f64, not on the double's bits).
	{"call_times_lit", `function half(x: f64): f64 { return x / 2.0; }
function main(): i32 { return (half(9.0) * 2.0) as i32; }`}, // 9
	// an i32-returning call must NOT be mis-typed f64: annotate stamps "i32",
	// expr_is_f64 reads it and answers false, keeping integer ops integer.
	{"i32_call_not_f64", `function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return add(add(10, 20), 12); }`}, // 42
}

func annotateF64ProjDir(t *testing.T) (dir, mmc, stdlibRoot string, gcc string, runner []string, interpBin string) {
	t.Helper()
	gcc, runner = x86_64Tooling(t)
	interpBin = buildLangBinForInterp(t)
	dir = writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc = buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")
	var err error
	stdlibRoot, err = filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	return dir, mmc, stdlibRoot, gcc, runner, interpBin
}

// TestSelfHostAnnotateF64IR_X86_64 pins the typed-IR annotation feeding
// the lowering through the self-host x86-64 IR path (#5531 slice 2).
func TestSelfHostAnnotateF64IR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range annotateF64Cases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			// The annotation is consumed on the IR path; assert the module
			// routes there so the case keeps exercising c.ty.
			route, derr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR annotate path)", tc.name, got)
			}

			asm, cerr := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "annf64_"+tc.name, string(asm))
			cmd := runX86_64Bin(runner, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s (IR annotate path) exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
