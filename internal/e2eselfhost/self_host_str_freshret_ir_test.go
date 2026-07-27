package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// strFreshRetIRCases pin the fresh-string-return-CALL reclaim on the self-hosted
// stack-IR path (#2649). A `var r = f(..)` whose callee f ALWAYS returns a freshly
// allocated string (str_fresh_ret_fns_of: every return is a concat / string method
// / named producer) means r solely owns the box f moved out — so r is reclaimed at
// scope exit (and per loop-rebind), closing the caller-side leak the returned
// builder left behind. A function that returns a param / field / literal / bare
// accumulator ident is NOT classified fresh, so its result is left to leak (sound).
var strFreshRetIRCases = []struct {
	name        string
	src         string
	expected    int
	mustReclaim bool
}{
	// f returns a concat (fresh); r = fmt(42) reclaimed. "n=42" len 4.
	{"freshret-concat",
		`function fmt(n: i32): string { return "n=" + i32_to_string(n); } function main(): i32 { var r: string = fmt(42); return r.len(); }`,
		4, true},
	// Un-annotated binding of a fresh-string-returning method-forwarder. len 3.
	{"freshret-unannotated",
		`function up(s: string): string { return s.to_ascii_upper(); } function main(): i32 { var r = up("abc"); return r.len(); }`,
		3, true},
	// Memory-safety at scale: r = build() reclaimed every iteration (flat heap). A
	// double-free would corrupt the freelist and crash / return garbage. exit 0.
	{"freshret-churn-safe",
		`function build(n: i32): string { return "x=" + i32_to_string(n); } function main(): i32 { var t: i32 = 0; var k: i32 = 0; while (k < 3000000) { var r: string = build(k); if (r.len() < 3) { t = 1; } k = k + 1; } return t; }`,
		0, true},
	// NEGATIVE: id() returns its PARAM (an alias of the caller's arg), so it is not
	// fresh-returning — r must NOT be reclaimed (freeing it could double-free the
	// caller-owned box). No __fern_str_free; value stays correct. len 2.
	{"freshret-return-param-not-reclaimed",
		`function id(s: string): string { return s; } function main(): i32 { var r: string = id("xy"); return r.len(); }`,
		2, false},
}

// TestSelfHostStrFreshRetIRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR default-on), asserting the exit code and that the fresh-ret
// -call reclaim (call __fn___fern_str_free) is (or isn't) emitted.
func TestSelfHostStrFreshRetIRX86_64(t *testing.T) {
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

	for _, tc := range strFreshRetIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			reclaims := countUserStrFreeReclaims(asm)
			if tc.mustReclaim && reclaims == 0 {
				t.Errorf("%s: expected a fresh-ret-call reclaim (call __fn___fern_str_free), found none — r leaks", tc.name)
			}
			if !tc.mustReclaim && reclaims != 0 {
				t.Errorf("%s: expected NO reclaim (callee not fresh-returning), found %d — a double-free / UAF risk", tc.name, reclaims)
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
