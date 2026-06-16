package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// strbufIRCases exercise the global string-builder builtins (strbuf_reset /
// strbuf_append / strbuf_take) on the IR path. They lower to dedicated
// strbuf_* IR ops (NOT a call_direct, so they sidestep the call eligibility
// gate that previously bailed asmcore.EmitState.write to the AST emitter) which
// each register backend emits as a call into the same __fern_strbuf_* runtime
// the AST path uses. strbuf_take snapshots the builder into a fresh string box;
// stdout pins the exact accumulated bytes.
var strbufIRCases = []struct {
	name, src, want string
}{
	{"build", `function main(): i32 { strbuf_reset(); strbuf_append("ab"); strbuf_append("cd"); write(strbuf_take()); return 0; }`, "abcd"},
	{"empty-take", `function main(): i32 { strbuf_reset(); write(strbuf_take()); write("end"); return 0; }`, "end"},
	{"reset-mid", `function main(): i32 { strbuf_reset(); strbuf_append("x"); strbuf_reset(); strbuf_append("y"); write(strbuf_take()); return 0; }`, "y"},
	{"take-into-var", `function main(): i32 { strbuf_reset(); strbuf_append("hello"); var s: string = strbuf_take(); write(s); return 0; }`, "hello"},
	// strbuf_take() returns a string box, so its .len() dispatches as a string —
	// proving expr_is_str tracks the result (the return value also drops cleanly).
	{"take-len", `function main(): i32 { strbuf_reset(); strbuf_append("abcde"); var s: string = strbuf_take(); return s.len(); }`, ""},
}

// TestSelfHostStrbufIRX86_64 — the x86-64 self-host emitter lowers a strbuf
// program through the IR path (asm_run with use_ir on) and the resulting binary
// accumulates + emits the expected bytes. The "take-len" case asserts via exit
// code (s.len() == 5) instead of stdout.
func TestSelfHostStrbufIRX86_64(t *testing.T) {
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
	for _, tc := range strbufIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if !bytes.Contains(asm, []byte("call __fern_strbuf_take")) {
				t.Fatalf("%s: no call to __fern_strbuf_take — did not lower through the IR path", tc.name)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			out, _ := cmd.Output()
			if tc.name == "take-len" {
				if code := cmd.ProcessState.ExitCode(); code != 5 {
					t.Errorf("%s: exit %d, want 5", tc.name, code)
				}
				return
			}
			if string(out) != tc.want {
				t.Errorf("%s: stdout %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}

// TestSelfHostStrbufIRArm64 — the arm64 counterpart (CI-gated, qemu-aarch64):
// the aarch64 IR emitter lowers strbuf through `bl __fern_strbuf_*` (the heap
// runtime, pulled in via mark_heap) and the binary produces the same bytes.
func TestSelfHostStrbufIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64.fern", "asm_arm64_ir.fern",
		"asm_arm64_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_ir_run.fern", "driver")
	for _, tc := range strbufIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !bytes.Contains(asm, []byte("bl __fern_strbuf_take")) {
				t.Fatalf("%s: no bl __fern_strbuf_take — did not lower through the arm64 IR path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "sb_"+tc.name, string(asm))
			rc := runArm64Bin(qemu, bin)
			out, _ := rc.Output()
			if tc.name == "take-len" {
				if code := rc.ProcessState.ExitCode(); code != 5 {
					t.Errorf("%s: exit %d, want 5", tc.name, code)
				}
				return
			}
			if string(out) != tc.want {
				t.Errorf("strbuf arm64 IR %q: stdout %q, want %q", tc.name, string(out), tc.want)
			}
		})
	}
}
