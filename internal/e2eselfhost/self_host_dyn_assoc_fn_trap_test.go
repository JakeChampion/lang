package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A trait ASSOCIATED function reached through a `dyn` value. Both checkers
// reject the call (E021), but E021 is a known self-host checker gap and so
// does not gate the self-host compile path — the emitters have to be safe on
// their own. They were not: parser.dyn_arm_matches keyed a dispatch arm on the
// method NAME, and finalize_impl_method gives an associated function the impl
// type as its receiver_type, so `make` matched and was called with the dyn
// value prepended as an argument it does not take. `P { v: 0 }` was read as
// the `Box` the body projects, so the program answered 0 where 7 is the only
// number the source can mean (#7398).
//
// With the arm excluded no arm matches, and the dispatch chain's fall-through
// — unreachable for a well-typed dyn value — aborts (134) instead of pushing a
// zero nobody can distinguish from an answer.
const dynAssocFnSrc = `struct Box { v: i32 }
trait Mk { function make(own b: Box): i32; }
struct P { v: i32 }
impl Mk for P { function make(own b: Box): i32 { return b.v; } }
function twice(own m: dyn Mk, b1: Box, b2: Box): i32 { return m.make(b1) + m.make(b2); }
function main(): i32 { return twice(P { v: 0 }, Box { v: 3 }, Box { v: 4 }); }`

// The `self`-taking twin: an ordinary method through the same dyn shape must
// still dispatch and answer, so the exclusion cannot widen into real methods.
const dynSelfMethodSrc = `struct Box { v: i32 }
trait Mk { function make(self: Self, b: Box): i32; }
struct P { v: i32 }
impl Mk for P { function make(self: Self, b: Box): i32 { return b.v + self.v; } }
function twice(m: dyn Mk, b1: Box, b2: Box): i32 { return m.make(b1) + m.make(b2); }
function main(): i32 { return twice(P { v: 0 }, Box { v: 3 }, Box { v: 4 }); }`

var dynAssocFnCases = []struct {
	name string
	src  string
	want int
}{
	{"assoc-fn-traps", dynAssocFnSrc, 134},
	{"self-method-dispatches", dynSelfMethodSrc, 7},
}

// TestSelfHostDynAssocFnDispatchX86_64 — the production x86-64 IR path.
func TestSelfHostDynAssocFnDispatchX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range dynAssocFnCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "dynassoc_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostDynAssocFnDispatchArm64 — the same cases through the arm64
// emit, which spells the fall-through trap with its own exit sequence.
func TestSelfHostDynAssocFnDispatchArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range dynAssocFnCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "dynassoc_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostDynAssocFnDispatchWasmIR — the wasm-IR leg. Its fall-through is
// `unreachable` rather than an exit syscall (the arms sit in a nested
// if/else chain whose result type it has to satisfy), which wasmtime
// surfaces as the same 134.
func TestSelfHostDynAssocFnDispatchWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR dyn associated-function e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range dynAssocFnCases {
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
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "dynassoc_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
