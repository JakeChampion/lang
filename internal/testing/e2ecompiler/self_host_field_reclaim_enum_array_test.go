package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var selfHostFieldReclaimEnumArrayCases = []struct {
	name string
	src  string
}{
	{"enum-array-field-reclaim", "enum Payload { None, Some(i32[]) }\nstruct Asm { ps: Payload[], n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, ps: [Payload.Some([v])] };\n}\nfunction main(): i32 {\n    let a: Asm = Asm { ps: [Payload.Some([9])], n: 0 };\n    a = step(a, 1);\n    return a.ps.len() + __rc_underflow_count();\n}"},

	{"enum-array-two-rc-variants", "enum Payload { None, Some(i32[]), Many(i32[]) }\nstruct Asm { ps: Payload[], n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, ps: [Payload.Some([v]), Payload.Many([v, v])] };\n}\nfunction main(): i32 {\n    let a: Asm = Asm { ps: [Payload.Some([9]), Payload.Many([8, 7])], n: 0 };\n    a = step(a, 1);\n    return a.ps.len() + __rc_underflow_count();\n}"},

	{"enum-array-three-rounds", "enum Payload { None, Some(i32[]) }\nstruct Asm { ps: Payload[], n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, ps: [Payload.Some([v])] };\n}\nfunction main(): i32 {\n    let a: Asm = Asm { ps: [Payload.Some([9])], n: 0 };\n    a = step(a, 1);\n    a = step(a, 2);\n    a = step(a, 3);\n    return a.ps.len() + __rc_underflow_count();\n}"},

	{"enum-array-via-struct-drop", "enum Payload { None, Some(i32[]) }\nstruct Inner { ps: Payload[] }\nstruct Asm { i: Inner, n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, i: Inner { ps: [Payload.Some([v])] } };\n}\nfunction main(): i32 {\n    let a: Asm = Asm { i: Inner { ps: [Payload.Some([9])] }, n: 0 };\n    a = step(a, 1);\n    return a.i.ps.len() + __rc_underflow_count();\n}"},

	{"shared-via-second-struct-declines-walk", "enum Payload { None, Some(i32[]) }\nstruct Asm { ps: Payload[], n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, ps: [Payload.Some([v])] };\n}\nfunction main(): i32 {\n    let a: Asm = Asm { ps: [Payload.Some([9, 9, 9])], n: 0 };\n    let b: Asm = Asm { ps: a.ps, n: 1 };\n    a = step(a, 1);\n    let r: i32 = 0;\n    match (b.ps[0]) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g.len(); } }\n    return r + __rc_underflow_count();\n}"},

	{"scalar-payload-enum-array-control", "enum Payload { None, Some(i32) }\nstruct Asm { ps: Payload[], n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, ps: [Payload.Some(v)] };\n}\nfunction main(): i32 {\n    let a: Asm = Asm { ps: [Payload.Some(0)], n: 0 };\n    a = step(a, 1);\n    return a.ps.len() + __rc_underflow_count();\n}"},

	{"struct-array-field-reclaim-control", "struct Slot { xs: i32[] }\nstruct Asm { cs: Slot[], n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, cs: [Slot { xs: [v] }] };\n}\nfunction main(): i32 {\n    let a: Asm = Asm { cs: [Slot { xs: [9] }], n: 0 };\n    a = step(a, 1);\n    return a.cs.len() + __rc_underflow_count();\n}"},
}

func TestSelfHostFieldReclaimEnumArrayLeakCheck(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "driver")

	for _, tc := range selfHostFieldReclaimEnumArrayCases {
		t.Run(tc.name, func(t *testing.T) {
			name := "frealeak_" + tc.name
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

func TestSelfHostFieldReclaimEnumArrayX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldReclaimEnumArrayCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "frea_"+tc.name, string(asm))
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

func TestSelfHostFieldReclaimEnumArrayArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldReclaimEnumArrayCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "frea_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostFieldReclaimEnumArrayWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldReclaimEnumArrayCases {
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
			watFile := filepath.Join(dir, "frea_"+tc.name+".wat")
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
