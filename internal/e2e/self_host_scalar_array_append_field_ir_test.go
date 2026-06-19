package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scalarArrayAppendFieldIRCases exercise a SCALAR-array (`i32[]`) struct-literal
// field VALUE built by `.append` / `.with` on a bare-ident LOCAL or PARAM
// receiver — `S { flags: flags.append(v) }`, not only on a field read. This is
// the last construct parser.dl_collect_stmt needed: it returns
// `DeferAcc { stmts, actions, flags }` where `flags: i32[]` is set via
// `flags.append(d.on_error)` on its borrowed param, alongside the enum-array
// stmts/actions fields (#3431). With this admitted, parser.dl_collect_stmt
// flips BAIL → ir and the whole parser module lowers via the IR path (0 bails).
//
// Soundness mirrors the field-read `.append` case already admitted: a scalar
// field's drop is the rc-gated buffer-only __fern_arr_dec, so an in-place grow
// (which bumps the buffer rc to 2, shared with the receiver) is dec'd-not-freed
// (leak) while a copy-grow yields a sole-owned rc=1 buffer that frees — no
// over-release either way. Each program builds the field and reads it back, so
// an over-release (garbage element) or a bail (AST fallback) shows in the exit
// code.
var scalarArrayAppendFieldIRCases = []struct {
	name string
	src  string
	want int
}{
	// `.append` on a borrowed param (the dl_collect_stmt `flags` shape).
	{"append-param", `struct S { xs: i32[], k: i32 }
function build(items: i32[], v: i32): S { return S { xs: items.append(v), k: items.len() }; }
function main(): i32 { var a: i32[] = [4, 5]; var s: S = build(a, 6); return s.xs[2] + s.k; }`, 8},

	// `.append` on a bare local.
	{"append-local", `struct S { xs: i32[], k: i32 }
function f(): S { var a: i32[] = [1, 2]; return S { xs: a.append(3), k: 9 }; }
function main(): i32 { var s: S = f(); return s.xs[0] + s.xs[1] + s.xs[2] + s.k; }`, 15},

	// `.with` on a borrowed param.
	{"with-param", `struct S { xs: i32[], k: i32 }
function build(items: i32[]): S { return S { xs: items.with(0, 7), k: 1 }; }
function main(): i32 { var a: i32[] = [3, 4]; var s: S = build(a); return s.xs[0] + s.xs[1] + s.k; }`, 12},
}

func TestSelfHostScalarArrayAppendFieldIRX86_64(t *testing.T) {
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
	for _, tc := range scalarArrayAppendFieldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 25000 {
				t.Fatalf("%s: asm is %d bytes — expected the compact IR output, not the AST runtime (a bail)", tc.name, len(asm))
			}
			progBin := buildBin(t, gcc, dir, "saf_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostScalarArrayAppendFieldIRArm64 runs the same cases through the
// arm64 IR backend (asm_arm64_run → emit_body, sharing irlower). The `.Lira_`
// marker pins IR routing; qemu pins correctness.
func TestSelfHostScalarArrayAppendFieldIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, _ := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64.fern", "asm_arm64_ir.fern",
		"asm_arm64_run.fern",
	} {
		s, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), s, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_run.fern", "driver")
	for _, tc := range scalarArrayAppendFieldIRCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(driverBin)
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("%s: driver failed (%d bytes, err %v)", tc.name, len(asm), err)
			}
			if !strings.Contains(string(asm), ".Lira_") {
				t.Fatalf("%s: arm64 asm has no .Lira_ marker — module bailed to the AST path", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "saf_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: exit %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
