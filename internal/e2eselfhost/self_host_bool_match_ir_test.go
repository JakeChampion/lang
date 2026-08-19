package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// boolMatchIRCases exercise `match` on a boolean scrutinee — `match (b) { true =>
// ..., false => ... }` — through the IR path. The scrutinee is already a 0/1
// value in a slot (no tag to extract), so each arm compares it to the wanted
// value, skips on mismatch, runs the body, and exits the match (mirroring the
// Option-tag arm), rather than bailing.
//
// Each program declares a fresh, non-escaping struct temp whose IR-only reclaim
// free (`call __fn___fern_arr_dec`; a bailing module emits none at all,
// and no array appears) proves the module took the IR path with the
// boolean-match lowering exercised. `t.x - t.y` pads 0 into the result. Exit
// codes pin the matched arm.
var boolMatchIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// match on a computed boolean (comparison). classify(8)=1, classify(2)=0 -> 10.
	{"bool-match-computed",
		`struct Point { x: i32, y: i32 } function classify(n: i32): i32 { match (n > 5) { true => { return 1; }, false => { return 0; } } return 9; } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; return classify(8) * 10 + classify(2) + pad; }`,
		10},
	// match on a boolean parameter. pick(true)=7, pick(false)=3 -> 10.
	{"bool-match-param",
		`struct Point { x: i32, y: i32 } function pick(b: boolean): i32 { match (b) { true => { return 7; }, false => { return 3; } } return 9; } function main(): i32 { var t: Point = Point { x: 2, y: 2 }; var pad: i32 = t.x - t.y; return pick(true) + pick(false) + pad; }`,
		10},
	// false-arm first (ordering independence). h(false)=4, h(true)=8 -> 12.
	{"bool-match-false-first",
		`struct Point { x: i32, y: i32 } function h(b: boolean): i32 { match (b) { false => { return 4; }, true => { return 8; } } return 0; } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; return h(false) + h(true) + pad; }`,
		12},
	// guard on a boolean arm (bind-before-guard shape, no binding). g(20)=2, g(5)=1,
	// g(0)=0 -> 2*100 + 1*10 + 0 = 210.
	{"bool-match-guard",
		`struct Point { x: i32, y: i32 } function g(n: i32): i32 { match (n > 0) { true when n > 10 => { return 2; }, true => { return 1; }, false => { return 0; } } return 9; } function main(): i32 { var t: Point = Point { x: 3, y: 3 }; var pad: i32 = t.x - t.y; return g(20) * 100 + g(5) * 10 + g(0) + pad; }`,
		210},
}

// TestSelfHostBoolMatchIRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR default-on), asserts the IR path was taken (the reclaimed-
// temp struct free), and asserts the matched value.
func TestSelfHostBoolMatchIRX86_64(t *testing.T) {
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

	for _, tc := range boolMatchIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if bytes.Count(asm, []byte("call __fn___fern_arr_dec")) == 0 {
				t.Fatalf("%s: no struct free emitted — the boolean-match IR path was NOT exercised", tc.name)
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
