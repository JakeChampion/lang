package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// binderScopeCases pin what a name is BOUND by, which is the question
// astwalk.collect_bound_stmt answers for every capture analysis in the
// compiler. Both cases were rejected outright before #6993 slice three, and
// both are shapes the self-host's own sources do not contain — so the
// fixpoint, which only proves the compiler reproduces itself, could not see
// either.
//
// The oracle is the native interp rather than a hand-computed constant: these
// are programs whose right answer is "whatever the reference implementation
// says", and a differential states that directly.
var binderScopeCases = []struct {
	name string
	src  string
}{
	// A lambda inside an `n @ Tag(x)` arm reading the @-binding. The whole-value
	// binder rides the PATTERN and lower_stmt_match only materialises it as a
	// `var` later, so every AST-level capture pass ran before it existed:
	// collect_bound_stmt did not report `n` as bound, the free-variable filter
	// then declined to treat it as an enclosing local, and the lambda took the
	// no-capture lift with a bare `n` in its body —
	//
	//	FERN_STRICT_IR: __lam_0 (function value n not defined)
	//
	// Two halves had to agree for this to work: the binder set (astwalk) and
	// the capture's TYPE, which cap_type_in_stmts resolves from the scrutinee.
	{"at-binding-captured-by-a-lambda", `enum Box { Full(i32), Empty }

function total(b: Box): i32 { match (b) { Full(v) => { return v; }, Empty => { return 0; } } return 0; }

function f(b: Box): i32 {
  match (b) {
    n @ Full(v) => {
      var g: (i32) => i32 = function(d: i32): i32 { return total(n) + d; };
      return g(1);
    },
    Empty => { return 0; },
  }
  return 0;
}

function main(): i32 { return f(Full(5)); }`},
	// A tuple destructure binds its names comma-joined (`var (g, h)` arrives as
	// the single name "g,h"), so a walk that does not split it reports neither
	// `g` nor `h` as bound. asmcore's call gate carried its own binder walk that
	// did not split, and rejected a valid program:
	//
	//	error[E001]: in fn 'main' at 6:10: call to undefined function 'g'
	//
	// It now shares astwalk's walk, which has split since #5173.
	{"tuple-destructured-fn-values-are-bound", `function inc(n: i32): i32 { return n + 1; }
function dbl(n: i32): i32 { return n * 2; }
function pair(): ((i32) => i32, (i32) => i32) { return (inc, dbl); }
function main(): i32 {
  var (g, h) = pair();
  return g(1) + h(2);
}`},
}

// TestSelfHostBinderScopeIRX86_64 runs them through the production x86-64 IR
// path under strict IR, so a per-function bail fails rather than reaching the
// right answer by another route.
func TestSelfHostBinderScopeIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range binderScopeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
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
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s exited %d, native interp says %d", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostBinderScopeWasmIR is the wasm leg. The capture lift picks a
// different trampoline shape there, and a funcref type is structural, so a
// mis-typed capture traps instead of quietly reading the wrong slot.
func TestSelfHostBinderScopeWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host binder-scope wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range binderScopeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(wat) == 0 {
				t.Fatal("self-host wasm compiler emitted 0 bytes")
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watFile)
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s exited %d, native interp says %d", tc.name, got, want)
			}
		})
	}
}
