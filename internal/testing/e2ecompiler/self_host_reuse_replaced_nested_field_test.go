package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #8568: when the in-place reuse path repurposes an `own` parameter's box
// (__fern_rc_is_unique), the OLD value of a replaced nested-struct field is
// released deeply, not box-only, so the inner's fields are not stranded.
//
// The deep release is rc==1-gated per field, and a child carried into the new
// inner from the old one is retained by the construction (the struct literal's
// alias-inc), so it sits at rc 2 when the walk decs it and survives at 1 for
// the new owner. The own-shared-child row below is that case built
// deliberately. The release runs BEFORE the stores, so the walk reads the old
// inner while it is still intact.
//
// THE EXIT CODE DOES NOT MOVE, as with the rest of this family: the leakcheck
// leg is the gate, and the register/wasm legs are the miscompile guard on the
// deep walk.
var selfHostReuseReplacedNestedCases = []struct {
	name string
	src  string
}{
	// The reported shape: an `own` base superseding its nested-struct field,
	// then handing itself back.
	{"own-base-supersede-nested", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    a = Asm { ...a, cfi: CfiState { bad: [v], open: false } };\n    return a;\n}\nfunction main(): i32 {\n    let a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    a = step(a, 1);\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	// The hazard `shallow`'s comment named, built on purpose: the NEW inner
	// carries a child of the OLD inner. The deep walk runs over it now, and the
	// construction's retain is what keeps that from being an over-release — so
	// this row fails loudly (exit 99, not a leak) if the retain ever stops.
	{"own-base-new-inner-shares-child", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    let shared: i32[] = a.cfi.bad;\n    a = Asm { ...a, cfi: CfiState { bad: shared, open: true } };\n    return a;\n}\nfunction main(): i32 {\n    let a: Asm = Asm { cfi: CfiState { bad: [7], open: false }, n: 0 };\n    a = step(a, 1);\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	// The BORROWED sibling of the shared-child row. It already took the deep
	// arm, so it is the control that says the two base modes now agree rather
	// than that the own mode merely stopped leaking.
	{"borrowed-base-new-inner-shares-child", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    let shared: i32[] = a.cfi.bad;\n    return Asm { ...a, cfi: CfiState { bad: shared, open: true } };\n}\nfunction main(): i32 {\n    let a: Asm = Asm { cfi: CfiState { bad: [7], open: false }, n: 0 };\n    a = step(a, 1);\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	// Control: an ARRAY field in the same own-base shape. The buffer is the box,
	// so it never wanted the deep arm and must not have gained one.
	{"own-base-array-field-control", "struct Asm { bad: i32[], n: i32 }\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    a = Asm { ...a, bad: [v] };\n    return a;\n}\nfunction main(): i32 {\n    let a: Asm = Asm { bad: [], n: 0 };\n    a = step(a, 1);\n    return a.bad.len() + __rc_underflow_count();\n}"},

	// Control: the same supersede on a LOCAL, which never took the shallow arm.
	{"local-base-control", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\nfunction main(): i32 {\n    let a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    a = Asm { ...a, cfi: CfiState { bad: [1], open: false } };\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},
}

// TestSelfHostReuseReplacedNestedLeakCheck is the gate: clean under
// FERN_LEAKCHECK on BOTH compilers, native as the oracle.
func TestSelfHostReuseReplacedNestedLeakCheck(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "driver")

	for _, tc := range selfHostReuseReplacedNestedCases {
		t.Run(tc.name, func(t *testing.T) {
			name := "rrnleak_" + tc.name
			natV, natExit := nativeLeakVerdict(t, cli, dir, name, tc.src)
			shV, shExit := selfHostLeakVerdict(t, gcc, runner, driverBin, dir, name, tc.src)
			if natV != verdictClean {
				t.Fatalf("native is not clean on %s (%s, exit %d) — the oracle moved, re-derive before touching the self-host", tc.name, natV, natExit)
			}
			if shV != verdictClean {
				t.Errorf("self-host %s: %s (exit %d), want clean like native", tc.name, shV, shExit)
			}
			if shExit != natExit {
				t.Errorf("self-host %s exited %d under leakcheck, native %d", tc.name, shExit, natExit)
			}
		})
	}
}

// TestSelfHostReuseReplacedNestedX86_64 — the answers against the interpreter
// oracle. An over-release from the added walk lands here as exit 99, which is
// what makes the shared-child rows worth running on this leg as well.
func TestSelfHostReuseReplacedNestedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostReuseReplacedNestedCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "rrn_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostReuseReplacedNestedArm64 — the arm64 emit of the same lowering.
func TestSelfHostReuseReplacedNestedArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostReuseReplacedNestedCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "rrn_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostReuseReplacedNestedWasmIR — the wasm leg emits its own reuse
// sequence from the same IR, so the added drop needs proving there too.
func TestSelfHostReuseReplacedNestedWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range selfHostReuseReplacedNestedCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("wasm driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "rrn_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
