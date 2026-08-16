package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// printStrIRCases exercise the `write(s)` / `print(s)` string-output builtins on
// the IR path. They lower to a `print_str` IR op (not a call_direct, so they
// sidestep the call eligibility gate). The register backends emit a call to
// __fn___fern_print_str, the Fern-compiled helper (asmcore.rt_src_print_str);
// wasm calls its own WASI $__fern_print_str. `print(s)` desugars to
// `print_str(s + "\n")` via the existing str_concat op, so one op + helper serves
// both. stdout pins the exact bytes: write is verbatim, print appends a newline.
var printStrIRCases = []struct {
	name, src, want string
}{
	{"write-literal", `function main(): i32 { write("hi"); return 0; }`, "hi"},
	{"print-literal", `function main(): i32 { print("hi"); return 0; }`, "hi\n"},
	{"write-var", `function main(): i32 { var s: string = "abc"; write(s); return 0; }`, "abc"},
	{"print-multi", `function main(): i32 { print("a"); print("b"); return 0; }`, "a\nb\n"},
	{"write-concat", `function main(): i32 { var s: string = "x" + "y"; write(s); return 0; }`, "xy"},
}

func TestSelfHostPrintStrIRX86_64(t *testing.T) {
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
	for _, tc := range printStrIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte("call __fn___fern_print_str")) {
				t.Fatalf("%s: no call to __fn___fern_print_str — did not lower through the IR path", tc.name)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			out, _ := cmd.Output()
			if string(out) != tc.want {
				t.Errorf("%s: stdout %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}

func TestSelfHostPrintStrIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern",
		"asm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range printStrIRCases {
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
			if !bytes.Contains(asm, []byte("bl __fn___fern_print_str")) {
				t.Fatalf("%s: no bl __fn___fern_print_str — did not lower through the arm64 IR path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "ps_"+tc.name, string(asm))
			out, _ := runArm64Bin(qemu, bin).Output()
			if string(out) != tc.want {
				t.Errorf("print_str arm64 IR %q: stdout %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}

func TestSelfHostPrintStrIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host print_str wasm IR e2e")
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
	for _, tc := range printStrIRCases {
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
			if !bytes.Contains(wat, []byte("$__fern_print_str")) {
				t.Fatalf("%s: no $__fern_print_str in wat — did not lower through the wasm IR path", tc.name)
			}
			watFile := filepath.Join(dir, "ps_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			out, _ := exec.Command("wasmtime", "run", watFile).Output()
			if string(out) != tc.want {
				t.Errorf("print_str wasm IR %q: stdout %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}
