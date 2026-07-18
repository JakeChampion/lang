package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// numericMethodsIRCases are self-contained programs exercising the i64 / u32
// numeric-method LOGIC (abs / min / max / clamp + unsigned compare) that
// std/i64 and std/u32 wrap, verified through the self-hosted compiler's x86-64
// IR path. The single-program self-host driver resolves no imports, so the
// method bodies are inlined — this verifies that the language constructs the
// stdlib methods compile to (64-bit and unsigned arithmetic / compare / branch
// across function calls) lower correctly on the IR path.
//
// The u32 case deliberately uses a value above 2^31 (signed-negative) so a
// signed comparison would give the wrong answer — confirming the IR path
// selects the unsigned compare. Each program's exit code is oracle-checked
// against the reference interpreter rather than hardcoded, so a wrong-but-
// stable result can't slip through (cf. the hardcoded-expectation gap in
// #2908). Goal-1 self-host IR coverage; FEATURE-AUDIT std/i64 · std/u32 rows.
//
// Scope note: the wasm IR backend still lowers u32/u64 comparisons as SIGNED
// (this test surfaced it) and a >2^63 u64 value built by addition is not yet
// IR-eligible on x86 — both tracked as a follow-up, so this test stays on the
// x86-64 IR path where the unsigned semantics are correct.
var numericMethodsIRCases = []struct {
	name string
	src  string
}{
	// i64 abs / min / max / clamp, composed across helper calls.
	// 7 + 5 + 9 = 21, then clamp(12,3,9)==9 adds 100 → 121.
	{"i64-abs-min-max-clamp", `function i64_abs(n: i64): i64 { if (n < (0 as i64)) { return (0 as i64) - n; } return n; }
function i64_min(a: i64, b: i64): i64 { if (a < b) { return a; } return b; }
function i64_max(a: i64, b: i64): i64 { if (a > b) { return a; } return b; }
function main(): i32 {
    var r: i64 = i64_abs(0 as i64 - 7 as i64) + i64_min(5 as i64, 9 as i64) + i64_max(5 as i64, 9 as i64);
    if (i64_max(i64_min(12 as i64, 9 as i64), 3 as i64) == 9 as i64) { r = r + 100 as i64; }
    return r as i32;
}`},
	// u32 min / max with a value above 2^31 (signed-negative): unsigned
	// max(4e9, 1) == 4e9 and min == 1. A signed compare would invert both.
	{"u32-unsigned-min-max", `function u32_min(a: u32, b: u32): u32 { if (a < b) { return a; } return b; }
function u32_max(a: u32, b: u32): u32 { if (a > b) { return a; } return b; }
function main(): i32 {
    var big: u32 = 4000000000 as u32; var one: u32 = 1 as u32;
    if (u32_max(big, one) == big && u32_min(big, one) == one) { return 42; }
    return 0;
}`},
	// i64 bit-op family (the std/i64 additions): count_zeros / leading_zeros /
	// trailing_zeros / rotate_left / rotate_right, over u64 shifts & masks.
	// Exercises 64-bit shift / and / or / compare across function calls on the
	// IR path; oracle-checked, so the wide-shift logic must be correct, not just
	// stable. count_zeros(7)=61, then four predicate hits → 65.
	{"i64-bitops", `function count_ones(n: i64): i32 {
    var u: u64 = n as u64; var c: i32 = 0; var i: i32 = 0;
    while (i < 64) { if ((u & (1 as u64)) != (0 as u64)) { c = c + 1; } u = u >> (1 as u64); i = i + 1; }
    return c;
}
function count_zeros(n: i64): i32 { return 64 - count_ones(n); }
function leading_zeros(n: i64): i32 {
    if (n == (0 as i64)) { return 64; }
    var u: u64 = n as u64; var top: u64 = (1 as u64) << (63 as u64); var c: i32 = 0;
    while ((u & top) == (0 as u64)) { c = c + 1; u = u << (1 as u64); }
    return c;
}
function trailing_zeros(n: i64): i32 {
    if (n == (0 as i64)) { return 64; }
    var u: u64 = n as u64; var c: i32 = 0;
    while ((u & (1 as u64)) == (0 as u64)) { c = c + 1; u = u >> (1 as u64); }
    return c;
}
function rotl(n: i64, bits: i32): i64 {
    var k: i32 = bits & 63; if (k == 0) { return n; }
    var u: u64 = n as u64;
    var left: u64 = u << (k as u64); var right: u64 = u >> ((64 - k) as u64);
    return (left | right) as i64;
}
function rotr(n: i64, bits: i32): i64 {
    var k: i32 = bits & 63; if (k == 0) { return n; }
    var u: u64 = n as u64;
    var right: u64 = u >> (k as u64); var left: u64 = u << ((64 - k) as u64);
    return (left | right) as i64;
}
function main(): i32 {
    var r: i32 = count_zeros(7 as i64);                             // 61
    if (leading_zeros(1 as i64) == 63) { r = r + 1; }              // 62
    if (trailing_zeros(8 as i64) == 3) { r = r + 1; }             // 63
    if (rotl(1 as i64, 1) == (2 as i64)) { r = r + 1; }          // 64
    if (rotr((1 as i64) << (63 as i64), 63) == (1 as i64)) { r = r + 1; }   // 65
    return r;
}`},
}

// TestSelfHostNumericMethodsIRX86_64 routes each case through the self-hosted
// x86-64 driver (IR on), asserts the exit code matches the interpreter oracle,
// AND probes the routing (asm_pathprobe_run) to pin each case to the "ir" path.
func TestSelfHostNumericMethodsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range numericMethodsIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
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
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
