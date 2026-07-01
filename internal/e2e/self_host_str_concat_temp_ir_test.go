package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// strConcatTempIRCases pin the anonymous-temporary reclaim of FRESH string concat
// OPERANDS on the self-hosted stack-IR path (#2649). In `X + Y`, an operand that is
// a fresh anonymous temp — a string literal (`s + "x"`: const_str allocs a box) or
// a fresh producer (a nested concat `a + b + c`, a method, a named producer) — is
// dead the instant the concat has read it, so emit_str_concat_reclaim frees it via
// __fern_str_free right after. A bare-ident / field operand (an ALIAS) is excluded.
var strConcatTempIRCases = []struct {
	name        string
	src         string
	expected    int
	mustReclaim bool
}{
	// Two literal operands: both freed after the concat. len 3.
	{"two-literals",
		`function main(): i32 { var r: string = "hi" + "!"; return r.len(); }`,
		3, true},
	// Concat chain: the intermediate (a + b) is a fresh temp freed after the outer
	// concat consumes it; a/b/c (idents) are aliases, not freed as operands. len 6.
	{"chain-intermediate",
		`function main(): i32 { var a: string = "x"; var b: string = "yy"; var c: string = "zzz"; var r: string = a + b + c; return r.len(); }`,
		6, true},
	// Memory-safety at scale: a concat-chain temporary in a 5,000,000-iteration loop,
	// non-escaping — the intermediate, the literal, and the final are all reclaimed,
	// so the heap stays FLAT (a leak would explode it; a double-free would corrupt the
	// freelist and crash / return garbage). exit 0.
	{"chain-churn-safe",
		`function main(): i32 { var pre: string = "aa"; var suf: string = "bb"; var t: i32 = 0; var k: i32 = 0; while (k < 5000000) { var r: string = pre + "x" + suf; t = (t + r.len()) % 7; k = k + 1; } return 0; }`,
		0, true},
	// NEGATIVE: both operands are bare idents (aliases of live locals) and the concat
	// result is consumed inline (not bound to a reclaimable local), so NOTHING is
	// freed — an aliased operand must never be mis-freed. "ab"+"cde" = len 5.
	{"ident-operands-not-freed",
		`function main(): i32 { var a: string = "ab"; var b: string = "cde"; return (a + b).len(); }`,
		5, false},
}

// TestSelfHostStrConcatTempIRX86_64 compiles each case through the self-hosted
// x86-64 driver, asserting the exit code and that the anonymous-operand reclaim is
// (or isn't) emitted. The negative uses only ident operands and no other reclaimable
// string, so a `call __fn___fern_str_free` would be an aliased-operand over-release.
func TestSelfHostStrConcatTempIRX86_64(t *testing.T) {
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

	for _, tc := range strConcatTempIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			reclaims := bytes.Count(asm, []byte("call __fn___fern_str_free"))
			if tc.mustReclaim && reclaims == 0 {
				t.Errorf("%s: expected a fresh-operand reclaim (call __fn___fern_str_free), found none — the temp leaks", tc.name)
			}
			if !tc.mustReclaim && reclaims != 0 {
				t.Errorf("%s: expected NO reclaim (ident operands are aliases), found %d — an over-release / UAF risk", tc.name, reclaims)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
