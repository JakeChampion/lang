package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// u32InferredCmpIRCases pin the fix for #3537: an *inferred* unsigned local
// (`var a = X as u32` / `as u64`, with no explicit `: u32` annotation) must
// compare as UNSIGNED. Before the fix, irlower's StmtVar lowering only marked a
// slot u32/u64 from the explicit annotation, so an inferred binding kept the
// signed default and a later `a > b` emitted a signed IR compare — wrong once
// bit 31 (u32) / bit 63 (u64) is set. The register backends happened to be
// correct (the value sits zero-extended in a 64-bit GPR), so this only
// MIScompiled on wasm's true-32-bit `i32.gt_s`; routing/interp don't catch a
// wrong-value compile, hence the value-pinned IR regression. The x86-64 variant
// guards against a regression now that the fix marks inferred u32/u64 on every
// backend. Each program returns 1 (the comparison is true).
var u32InferredCmpIRCases = []struct {
	name string
	main string
	want int
}{
	// 3_000_000_000 (bit 31 set) > 5 — signed i32 would read it negative.
	{"u32-gt", `var a = 3000000000 as u32; var b = 5 as u32; if (a > b) { return 1; } return 0;`, 1},
	// same value on the right of a `<`.
	{"u32-lt", `var a = 3000000000 as u32; var b = 5 as u32; if (b < a) { return 1; } return 0;`, 1},
	// equal large values via `>=`.
	{"u32-ge", `var a = 3000000000 as u32; var b = 3000000000 as u32; if (a >= b) { return 1; } return 0;`, 1},
	// u64 sibling: 1.8e19 has bit 63 set — signed i64 would read it negative.
	{"u64-gt", `var a = 18000000000000000000 as u64; var b = 5 as u64; if (a > b) { return 1; } return 0;`, 1},
}

func u32InferredCmpIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostU32InferredCmpIRWasm is the primary regression for #3537 — the
// wasm IR backend is the one that miscompiled the signed compare.
func TestSelfHostU32InferredCmpIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host u32-inferred-cmp wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range u32InferredCmpIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(u32InferredCmpIRSrc(tc.main))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "u32_inferred_cmp_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("u32-inferred-cmp wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostU32InferredCmpIRX86_64 runs the same cases through the x86-64 IR
// driver (pinned to the "ir" path) — a guard that the inferred-u32 marking
// stays correct on the register backends too.
func TestSelfHostU32InferredCmpIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range u32InferredCmpIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(u32InferredCmpIRSrc(tc.main))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
