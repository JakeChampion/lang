package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// The remaining stdin/self-host compile drivers must enforce E048 before they
// emit any code. asm_load_run/fern.fern already gate the cycle rules; this test
// pins the same behaviour for the stdin-oriented backends so `p.x = v` can no
// longer compile on one code path while being rejected on another.
func TestSelfHostStdinDriversRejectFieldMutationX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	type driverCase struct {
		name  string
		build func(t *testing.T) string
		args  []string
	}
	buildAsmDir := func(t *testing.T) string {
		dir := writeSelfHostAsmProject(t)
		copySelfHostFiles(t, dir, "asm_run.fern")
		return buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	}
	buildAsmIRDir := func(t *testing.T) string {
		dir := writeSelfHostAsmProject(t)
		copySelfHostFiles(t, dir, "asm_ir_run.fern")
		return buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	}
	buildWasmDir := func(t *testing.T) string {
		dir := t.TempDir()
		copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm.fern", "wasm_run.fern")
		return buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "driver")
	}
	buildWasmIRDir := func(t *testing.T) string {
		dir := t.TempDir()
		copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern")
		return buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	}
	buildSSADir := func(t *testing.T) string {
		dir := t.TempDir()
		copySelfHostFiles(t, dir, "lexer.fern", "parser.fern", "astwalk.fern", "ssa.fern", "util.fern", "ssa_x86.fern", "ssa_arm64.fern", "ssa_emit_run.fern")
		return buildSelfHostBin(t, gcc, dir, "ssa_emit_run.fern", "driver")
	}

	for _, tc := range []driverCase{
		{name: "asm_run", build: buildAsmDir},
		{name: "asm_ir_run", build: buildAsmIRDir, args: []string{"-ir"}},
		{name: "wasm_run", build: buildWasmDir},
		{name: "wasm_ir_run", build: buildWasmIRDir, args: []string{"-ir"}},
		{name: "ssa_emit_run", build: buildSSADir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driver := tc.build(t)
			run := func(src string) ([]byte, string, error) {
				var cmd *exec.Cmd
				if len(runner) == 0 {
					cmd = exec.Command(driver, tc.args...)
				} else {
					cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driver), tc.args...)...)
				}
				cmd.Stdin = bytes.NewReader([]byte(src))
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				return out, stderr.String(), err
			}

			out, stderr, err := run("struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p.x = 5; return p.x; }\n")
			if err == nil {
				t.Fatalf("expected %s to reject field mutation, got success with %d bytes", tc.name, len(out))
			}
			if !strings.Contains(stderr, "error[E048]") {
				t.Fatalf("%s stderr missing E048:\n%s", tc.name, stderr)
			}

			out, stderr, err = run("struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p = P { ...p, x: 5 }; return p.x; }\n")
			if err != nil {
				t.Fatalf("%s rejected functional update: %v\nstderr: %s", tc.name, err, stderr)
			}
			if len(out) == 0 {
				t.Fatalf("%s emitted 0 bytes for valid functional update", tc.name)
			}
		})
	}
}
