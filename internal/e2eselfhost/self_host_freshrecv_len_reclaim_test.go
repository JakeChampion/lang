package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// freshRecvLenCases pin the fresh-or-RECEIVER method reclaim in `.len()`
// receiver position (#6544).
//
// A borrowing string method has one shape: return a fresh box, or return the
// receiver unchanged when there is nothing to do — `(s: string) tails(n)` is
// `if (n <= 0) { return s; }` and a slice otherwise. Freshness alone cannot
// admit that, and native's route is not open either: it reclaims the identity
// return through an is_unique gate resting on a return-transfer inc the
// self-host emits for array params only, and adding that inc UNPAIRED measures
// strictly worse (docs/RC-PERCEUS-SELF-HOST-PORT.md §9).
//
// The SFRRECV: key proves the callee returns fresh-or-receiver, and the
// POINTER decides which arrived: a fresh box is never the receiver's pointer,
// an identity return always is. So the read frees only when they differ — no
// inc anywhere.
//
// Both directions are pinned. The churn case proves the fresh box IS freed
// (heap-bump flat); the identity case proves the aliased one is NOT (the
// receiver survives being read afterwards, and __rc_underflow() stays 0 — a
// mis-free would either corrupt the value or tick the detector).
var freshRecvLenCases = []struct {
	name     string
	src      string
	expected int
}{
	// FRESH path freed: 5000 rounds stay flat. On the parent this leaked one
	// box per evaluation (48 bytes a round, measured).
	{"freshrecv-len-churn", `function (s: string) tails(n: i32): string {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen] + "";
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5000) { var b2s: string = "long-enough-payload-" + (i % 8).to_string(); acc = (acc + b2s.tails(4).len()) % 251; i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// IDENTITY path NOT freed: `b.tails(0)` hands back `b` itself, which is
	// read again afterwards. Freeing it would be a use-after-free; the value
	// check and the underflow counter are both witnesses.
	{"freshrecv-len-identity-alias-safe", `function (s: string) tails(n: i32): string {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen] + "";
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 3000) {
        var b: string = "long-enough-payload-" + (i % 8).to_string();
        if (b.tails(0).len() != b.len()) { return 96; }
        if (b.len() != 21) { return 95; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// Both paths in one body, alternating per iteration — the discriminator is
	// a runtime pointer compare, so it has to decide correctly each time rather
	// than once per call site.
	{"freshrecv-len-alternating", `function (s: string) tails(n: i32): string {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen] + "";
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5000) {
        var b3: string = "long-enough-payload-" + (i % 8).to_string();
        acc = (acc + b3.tails(i % 2 * 4).len()) % 251;
        if (b3.len() != 21) { return 96; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
}

// freshRecvLenLeakCase is the LEAK half, x86-64 only. Heap flatness is asserted
// here rather than in the shared table because the wasm leg runs the WAT driver
// (wasm_ir_run), and every wasm sibling in this package asserts the
// over-release detector rather than heap growth for that reason. Flatness on
// wasm was checked separately through the CLI pipeline (`-target wasm32-wasi`),
// where this churn moves the bump pointer by under 128 bytes across 5000
// rounds; the shared cases below carry wasm's half, which is that nothing is
// over-released and the aliased receiver survives.
const freshRecvLenLeakCase = `function (s: string) tails(n: i32): string {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen] + "";
}
function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var b: string = "long-enough-payload-" + (w % 8).to_string(); acc = (acc + b.tails(4).len()) % 251; w = w + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < 5000) { var c: string = "long-enough-payload-" + (i % 8).to_string(); acc = (acc + c.tails(4).len()) % 251; i = i + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`

// TestSelfHostFreshRecvLenReclaimX86_64 runs each case through the self-hosted
// x86-64 driver, plus the leak case. Exit 0 = reclaimed and safe; 98 = the
// fresh box leaked, 99 = over-release, 95/96 = a value went wrong.
func TestSelfHostFreshRecvLenReclaimX86_64(t *testing.T) {
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

	cases := append([]struct {
		name     string
		src      string
		expected int
	}{{"freshrecv-len-leak-flat", freshRecvLenLeakCase, 0}}, freshRecvLenCases...)
	for _, tc := range cases {
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
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d (98 = fresh box leaked; 99 = over-release; 95/96 = aliased receiver corrupted)", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostFreshRecvLenReclaimWasm runs the same cases on the wasm IR
// backend, where the release maps to $__fern_arr_dec and its over-release
// detector is the direct witness that the identity alias is never freed.
func TestSelfHostFreshRecvLenReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fresh-or-receiver len reclaim wasm e2e")
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

	for _, tc := range freshRecvLenCases {
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
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("fresh-or-receiver len reclaim wasm %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
