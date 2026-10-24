package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var selfHostArrEnumElemReadCases = []struct {
	name    string
	src     string
	refused bool
}{
	{"rc-payload-borrow-read", "enum Payload { None, Some(i32[]) }\nfunction main(): i32 {\n    let keep: Payload[] = [Payload.Some([9, 9, 9])];\n    let r: i32 = 0;\n    match (keep[0]) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g.len(); } }\n    return r + __rc_underflow_count();\n}", false},

	{"scalar-payload-stored-out", "enum Payload { None, Some(i32) }\nfunction main(): i32 {\n    let keep: Payload[] = [Payload.Some(3)];\n    let r: i32 = 0;\n    match (keep[0]) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g; } }\n    return r + __rc_underflow_count();\n}", false},

	{"loop-thirty-rounds", "enum Payload { None, Some(i32[]) }\nfunction main(): i32 {\n    let r: i32 = 0;\n    let i: i32 = 0;\n    while (i < 30) {\n        let keep: Payload[] = [Payload.Some([9, 9, 9])];\n        match (keep[0]) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g.len(); } }\n        i = i + 1;\n    }\n    return r + __rc_underflow_count();\n}", false},

	{"two-rc-variants-both-borrowed", "enum Payload { None, Some(i32[]), Many(i32[]) }\nfunction main(): i32 {\n    let keep: Payload[] = [Payload.Many([7, 7])];\n    let r: i32 = 0;\n    match (keep[0]) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g.len(); }, Payload.Many(h) => { r = h.len(); } }\n    return r + __rc_underflow_count();\n}", false},

	{"len-only-control", "enum Payload { None, Some(i32[]) }\nfunction main(): i32 {\n    let keep: Payload[] = [Payload.Some([9, 9, 9])];\n    return keep.len() + __rc_underflow_count();\n}", false},

	{"escaping-pointer-binding-stays-refused", "enum Payload { None, Some(i32[]) }\nfunction main(): i32 {\n    let out: i32[] = [];\n    let keep: Payload[] = [Payload.Some([9, 9, 9])];\n    match (keep[0]) { Payload.None => { }, Payload.Some(g) => { out = g; } }\n    return out.len() + __rc_underflow_count();\n}", true},
}

func TestSelfHostArrEnumElemReadLeakCheck(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "driver")

	for _, tc := range selfHostArrEnumElemReadCases {
		t.Run(tc.name, func(t *testing.T) {
			name := "aer_" + tc.name
			natV, natExit := nativeLeakVerdict(t, cli, dir, name, tc.src)
			shV, shExit := selfHostLeakVerdict(t, gcc, runner, driverBin, dir, name, tc.src)
			if natV != verdictClean {
				t.Fatalf("native is not clean on %s (%s, exit %d) — the oracle moved, re-derive before touching the self-host", tc.name, natV, natExit)
			}
			if tc.refused {
				if shV == verdictCrash {
					t.Errorf("refused row %s crashed (exit %d) — the shallow path must stay safe", tc.name, shExit)
				}
				if shExit != natExit {
					t.Errorf("refused row %s exited %d, native %d — the answer moved, so the element was released under a live alias", tc.name, shExit, natExit)
				}
				return
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

func TestSelfHostArrEnumElemReadX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostArrEnumElemReadCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "aer_"+tc.name, string(asm))
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

func TestSelfHostArrEnumElemReadArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range selfHostArrEnumElemReadCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "aer_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostArrEnumElemReadWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range selfHostArrEnumElemReadCases {
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
			watFile := filepath.Join(dir, "aer_"+tc.name+".wat")
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
