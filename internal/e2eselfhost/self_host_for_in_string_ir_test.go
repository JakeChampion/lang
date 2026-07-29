package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostForInStringIR covers `for b in STR` (iterate a string's bytes)
// through the self-hosted x86-64 compiler on the IR path. The self-host
// foreach assumed an array layout (length @0, elements @ base+idx*8+8), but a
// string is { data_ptr @0, len @8 } with byte elements, so it read the data
// pointer as the length and 8-byte-indexed the header (#2822 — the reproducer
// `for b in "AB"` returned 2 instead of 131). irlower now desugars a string
// foreach to a counted loop over STR.len() with a byte-index bind, reusing the
// range-for + string-index paths.
//
// Covered: string literals, string locals, byte sum / count, and break /
// continue. (A string-returning *call* or a *slice* iterable still routes
// through the array path — a separate string-tracking gap for those expression
// forms in foreach position — and is left as a follow-up.)
func TestSelfHostForInStringIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emitAndRunIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s", err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
	}

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"literal-byte-sum", `function main(): i32 { var s: i32 = 0; for b in "AB" { s = s + b; } return s; }`, 131}, // 'A'+'B' = 65+66
		{"literal-count", `function main(): i32 { var n: i32 = 0; for c in "hello" { n = n + 1; } return n; }`, 5},
		{"local", `function main(): i32 { var t: string = "AB"; var s: i32 = 0; for b in t { s = s + b; } return s; }`, 131},
		{"continue", `function main(): i32 { var s: i32 = 0; for b in "ABC" { if (b == 66) { continue; } s = s + b; } return s; }`, 132}, // skip 'B'
		{"break", `function main(): i32 { var s: i32 = 0; for b in "ABC" { if (b == 66) { break; } s = s + b; } return s; }`, 65},
		{"empty", `function main(): i32 { var s: i32 = 7; for b in "" { s = s + b; } return s; }`, 7},
		// A string-returning CALL / METHOD / SLICE as the iterable (#2822
		// follow-up): these route through the byte-foreach path now that the
		// eligibility probe threads str_ret_fns (so the iterable types as a
		// string instead of falling to the array-snapshot path + AST fallback).
		{"call-returning-string", `function greet(): string { return "AB"; } function main(): i32 { var s: i32 = 0; for b in greet() { s = s + b; } return s; }`, 131},
		{"method-returning-string", `struct B { tag: i32 } function (b: B) name(): string { return "AB"; } function main(): i32 { var x: B = B { tag: 1 }; var s: i32 = 0; for c in x.name() { s = s + c; } return s; }`, 131},
		{"slice", `function main(): i32 { var s: i32 = 0; for b in "ABCD"[1:3] { s = s + (b as i32); } return s; }`, 133}, // 'B'+'C' = 66+67
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emitAndRunIR(t, tc.src); got != tc.want {
				t.Errorf("self-host IR %q: exit = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
