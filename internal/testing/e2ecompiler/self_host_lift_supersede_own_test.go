package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var selfHostLiftSupersedeCases = []struct {
	name string
	src  string
}{
	{"lift-supersede-own", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(own s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    let st: CfiState = a.cfi;\n    a = Asm { ...a, cfi: CfiState { bad: [], open: false } };\n    st = directive(st, v);\n    return Asm { ...a, cfi: st };\n}\nfunction main(): i32 {\n    let a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    let i: i32 = 0;\n    while (i < 8) { a = step(a, i); i = i + 1; }\n    if (!a.cfi.open) { return 70; }\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	{"lift-supersede-own-full-literal", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(own s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    let st: CfiState = a.cfi;\n    a = Asm { cfi: CfiState { bad: [], open: false }, n: a.n };\n    st = directive(st, v);\n    return Asm { ...a, cfi: st };\n}\nfunction main(): i32 {\n    let a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    let i: i32 = 0;\n    while (i < 8) { a = step(a, i); i = i + 1; }\n    if (!a.cfi.open) { return 70; }\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	{"lift-supersede-own-straightline", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(own s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    let st: CfiState = a.cfi;\n    a = Asm { ...a, cfi: CfiState { bad: [], open: false } };\n    st = directive(st, v);\n    return Asm { ...a, cfi: st };\n}\nfunction main(): i32 {\n    let a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    a = step(a, 1);\n    a = step(a, 2);\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	{"carried-field-control", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(own s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    let st: CfiState = a.cfi;\n    a = Asm { ...a, n: a.n + 1 };\n    st = directive(st, v);\n    return Asm { ...a, cfi: st };\n}\nfunction main(): i32 {\n    let a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    let i: i32 = 0;\n    while (i < 8) { a = step(a, i); i = i + 1; }\n    if (!a.cfi.open) { return 70; }\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	{"borrowed-callee-control", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    let st: CfiState = a.cfi;\n    a = Asm { ...a, cfi: CfiState { bad: [], open: false } };\n    st = directive(st, v);\n    return Asm { ...a, cfi: st };\n}\nfunction main(): i32 {\n    let a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    let i: i32 = 0;\n    while (i < 8) { a = step(a, i); i = i + 1; }\n    if (!a.cfi.open) { return 70; }\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	{"no-call-control", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    let st: CfiState = a.cfi;\n    a = Asm { ...a, cfi: CfiState { bad: [], open: false } };\n    st = CfiState { ...st, bad: st.bad.append(v), open: true };\n    return Asm { ...a, cfi: st };\n}\nfunction main(): i32 {\n    let a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    let i: i32 = 0;\n    while (i < 8) { a = step(a, i); i = i + 1; }\n    if (!a.cfi.open) { return 70; }\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},
}

func TestSelfHostLiftSupersedeOwnX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostLiftSupersedeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "lso_"+tc.name, string(asm))
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

func TestSelfHostLiftSupersedeOwnArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostLiftSupersedeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "lso_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostLiftSupersedeOwnWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range selfHostLiftSupersedeCases {
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
			watFile := filepath.Join(dir, "lso_"+tc.name+".wat")
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
