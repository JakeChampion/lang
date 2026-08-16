package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// putcharIRCases exercise the `putchar(c)` byte-output builtin on the IR path.
// It lowers to a putchar IR op (not a call_direct, so it sidesteps the call
// eligibility gate). On x86-64 and arm64 the op calls __fn___fern_putchar, the
// Fern-source runtime helper (asmcore.rt_src_putchar, #2649) lowered through the
// same IR pipeline as user code; wasm keeps its own helper and inlines a scratch
// iovec + fd_write instead. stdout pins the exact bytes: putchar writes the one
// byte it is given and appends nothing.
//
// putchar(200) writes the raw byte 0xC8, not its UTF-8 encoding — all three
// backends agree. (`fern -interp` writes 0xC3 0x88 for the same program.)
var putcharIRCases = []struct {
	name, src, want string
}{
	{"ascii", `function main(): i32 { putchar(65); return 0; }`, "A"},
	{"sequence", `function main(): i32 { putchar(70); putchar(101); putchar(114); putchar(110); return 0; }`, "Fern"},
	{"newline", `function main(): i32 { putchar(10); return 0; }`, "\n"},
	{"high-byte", `function main(): i32 { putchar(200); return 0; }`, "\xc8"},
	{"const-expr", `function main(): i32 { putchar(60 + 5); return 0; }`, "A"},
	{"computed", `function main(): i32 { var c: i32 = 60; putchar(c + 5); return 0; }`, "A"},
}

// The wasm backend emits no putchar symbol, so pin the inlined sequence: stash
// the byte at scratch address 16, point the iovec (base@0, len@4) at it, write
// fd 1.
const wasmPutcharSeq = "    i32.store8\n" +
	"    i32.const 0\n    i32.const 16\n    i32.store\n" +
	"    i32.const 4\n    i32.const 1\n    i32.store\n" +
	"    i32.const 1\n    i32.const 0\n    i32.const 1\n    i32.const 8\n    call $fd_write\n"

func TestSelfHostPutcharIRX86_64(t *testing.T) {
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
	for _, tc := range putcharIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte("call __fn___fern_putchar")) {
				t.Fatalf("%s: no call to __fn___fern_putchar — did not lower through the IR path", tc.name)
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

func TestSelfHostPutcharIRArm64(t *testing.T) {
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
	for _, tc := range putcharIRCases {
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
			if !bytes.Contains(asm, []byte("bl __fn___fern_putchar")) {
				t.Fatalf("%s: no bl __fn___fern_putchar — did not lower through the arm64 IR path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "pc_"+tc.name, string(asm))
			out, _ := runArm64Bin(qemu, bin).Output()
			if string(out) != tc.want {
				t.Errorf("putchar arm64 IR %q: stdout %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}

func TestSelfHostPutcharIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host putchar wasm IR e2e")
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
	for _, tc := range putcharIRCases {
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
			if !bytes.Contains(wat, []byte(wasmPutcharSeq)) {
				t.Fatalf("%s: no inlined putchar fd_write sequence in wat — did not lower through the wasm IR path", tc.name)
			}
			watFile := filepath.Join(dir, "pc_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			out, _ := exec.Command("wasmtime", "run", watFile).Output()
			if string(out) != tc.want {
				t.Errorf("putchar wasm IR %q: stdout %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}
