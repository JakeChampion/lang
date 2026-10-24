package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var trailingCommaCases = []struct {
	name string
	src  string
	exit int
}{
	{"two-field", "struct P { a: i32, b: i32 } function main(): i32 { let p = P { a: 40, b: 2, }; return p.a + p.b; }", 42},
	{"one-field", "struct Q { v: i32 } function main(): i32 { let q = Q { v: 42, }; return q.v; }", 42},
	{"no-trailing-still-works", "struct R { a: i32, b: i32 } function main(): i32 { let r = R { a: 40, b: 2 }; return r.a + r.b; }", 42},
	{"string-field-trailing", "struct S { name: string, n: i32 } function main(): i32 { let s = S { name: \"hi\", n: 40, }; return s.name.len() + s.n; }", 42},
	{"fn-params-trailing", "function add3(a: i32, b: i32, c: i32,): i32 { return a + b + c; } function main(): i32 { return add3(35, 5, 2); }", 42},
	{"arrow-lambda-params-trailing", "function main(): i32 { let twice: (i32) => i32 = (n: i32,) => n * 2; return twice(21); }", 42},
	{"fn-keyword-lambda-params-trailing", "function main(): i32 { let f: (i32) => i32 = (a: i32,): i32 => { return a + 2; }; return f(40); }", 42},
	{"two-param-lambda-trailing", "function main(): i32 { let add: (i32, i32) => i32 = (a: i32, b: i32,) => a + b; return add(40, 2); }", 42},
	{"single-param-no-trailing-regress", "function id2(a: i32): i32 { return a; } function main(): i32 { let f: (i32) => i32 = (n: i32) => n; return id2(f(42)); }", 42},
}

func TestSelfHostTrailingCommaX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range trailingCommaCases {
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostTrailingCommaArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range trailingCommaCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostTrailingCommaWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host trailing-comma wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir,
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range trailingCommaCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != tc.exit {
				t.Fatalf("interp oracle %d disagrees with the table's %d — fix the case", want, tc.exit)
			}
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("self-host wasm compiler emitted 0 bytes")
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watFile)
			var se bytes.Buffer
			cmd.Stderr = &se
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally (invalid module — the bug):\n%s", se.String())
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d\nstderr: %s", tc.name, code, tc.exit, se.String())
			}
		})
	}
}
