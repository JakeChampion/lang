package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// eprintIRCases exercise the `eprint(s)` / `eprint_int(n)` stderr builtins on the
// IR path. They lower to an `eprint_str` IR op (not a call_direct, so they
// sidestep the call eligibility gate) that each backend emits as a call into the
// same __fern_eprint_str helper the AST path uses (write to fd 2, then a newline).
// `eprint` is the stderr line-printer — it appends "\n", mirroring `print` and the
// Go-backend / interp / wasm `eprint`, which all do. eprint_int desugars to
// i32_to_string then eprint_str. stderr pins the bytes.
var eprintIRCases = []struct {
	name, src, want string
}{
	{"eprint-literal", `function main(): i32 { eprint("hi"); return 0; }`, "hi\n"},
	{"eprint-var", `function main(): i32 { var s: string = "abc"; eprint(s); return 0; }`, "abc\n"},
	{"eprint-int", `function main(): i32 { eprint_int(42); return 0; }`, "42\n"},
	{"eprint-concat", `function main(): i32 { eprint("x" + "y"); return 0; }`, "xy\n"},
}

func TestSelfHostEprintIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	for _, tc := range eprintIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte("call __fern_eprint_str")) {
				t.Fatalf("%s: no call to __fern_eprint_str — did not lower through the IR path", tc.name)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			_ = cmd.Run()
			if stderr.String() != tc.want {
				t.Errorf("%s: stderr %q, want %q", tc.name, stderr.String(), tc.want)
			}
		})
	}
}

func TestSelfHostEprintIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern",
		"asm_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range eprintIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-target", "arm64-linux", "-ir")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64-linux", "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(asm, []byte("bl __fern_eprint_str")) {
				t.Fatalf("%s: no bl __fern_eprint_str — did not lower through the arm64 IR path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "ep_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			var stderr bytes.Buffer
			run.Stderr = &stderr
			_ = run.Run()
			if stderr.String() != tc.want {
				t.Errorf("eprint arm64 IR %s: stderr %q, want %q", tc.name, stderr.String(), tc.want)
			}
		})
	}
}

func TestSelfHostEprintIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host eprint wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	for _, tc := range eprintIRCases {
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
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(wat, []byte("$__fern_eprint_str")) {
				t.Fatalf("%s: no $__fern_eprint_str in wat — did not lower through the wasm IR path", tc.name)
			}
			watFile := filepath.Join(dir, "ep_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			var stderr bytes.Buffer
			run.Stderr = &stderr
			_ = run.Run()
			if stderr.String() != tc.want {
				t.Errorf("eprint wasm IR %s: stderr %q, want %q", tc.name, stderr.String(), tc.want)
			}
		})
	}
}
