package e2eselfhost

import (
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
	name     string
	src      string
	expected int
	// wantReclaims: -1 = at least one __fern_str_free reclaim site must be
	// emitted; N >= 0 = EXACTLY N sites (the ident-operand case pins 1: the
	// fresh RESULT temp's release — 3 would mean the aliased operands were
	// mis-freed too, the over-release this table exists to catch).
	wantReclaims int
}{
	// Two literal operands: both freed after the concat. len 3.
	{"two-literals",
		`function main(): i32 { var r: string = "hi" + "!"; return r.len(); }`,
		3, -1},
	// Concat chain: the intermediate (a + b) is a fresh temp freed after the outer
	// concat consumes it; a/b/c (idents) are aliases, not freed as operands. len 6.
	{"chain-intermediate",
		`function main(): i32 { var a: string = "x"; var b: string = "yy"; var c: string = "zzz"; var r: string = a + b + c; return r.len(); }`,
		6, -1},
	// Memory-safety at scale: a concat-chain temporary in a 5,000,000-iteration loop,
	// non-escaping — the intermediate, the literal, and the final are all reclaimed,
	// so the heap stays FLAT (a leak would explode it; a double-free would corrupt the
	// freelist and crash / return garbage). exit 0.
	{"chain-churn-safe",
		`function main(): i32 { var pre: string = "aa"; var suf: string = "bb"; var t: i32 = 0; var k: i32 = 0; while (k < 5000000) { var r: string = pre + "x" + suf; t = (t + r.len()) % 7; k = k + 1; } return 0; }`,
		0, -1},
	// Ident operands, result consumed inline by .len(): the fresh RESULT temp
	// is released (the #4365 value-consuming-receiver reclaim — exactly ONE
	// __fern_str_free site), while the aliased operands a/b are never freed
	// (a count of 3 — result + both operands — would be the over-release this
	// case exists to catch; the exit code proves the values survive).
	// "ab"+"cde" = len 5.
	{"ident-operands-result-only",
		`function main(): i32 { var a: string = "ab"; var b: string = "cde"; return (a + b).len(); }`,
		5, 1},
	// A scalar `.to_string()` operand (`"n" + w.to_string()`) is the builtin
	// fresh producer — freed after the concat (#4353 concat-temp finding: it
	// was the one producer is_fresh_str_temp missed, leaking the temp per
	// evaluation). Heap-bump flat across 5000 iterations.
	{"tostring-operand-churn",
		`function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { var r: string = "n" + w.to_string(); acc = (acc + r.len()) % 251; w = w + 1; }
    var b1: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < 5000) { var r2: string = "n" + i.to_string(); acc = (acc + r2.len()) % 251; i = i + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`,
		0, -1},
	// Value shape: "n" + 47.to_string() = "n47", len 3 (scalar-receiver
	// admission via the ident-slot arm; the cast arm gets its own case).
	{"tostring-operand-len",
		`function main(): i32 { var w: i32 = 47; return ("n" + w.to_string()).len(); }`,
		3, -1},
	// Cast-receiver form `(k as u32).to_string()` — the as_* scalar arm.
	{"tostring-cast-operand-len",
		`function main(): i32 { var k: i32 = 12; return ("v" + (k as u32).to_string()).len(); }`,
		3, -1},
	// NEGATIVE: a STRING-receiver `.to_string()` is the identity case — its
	// result aliases the receiver, so the operand must NOT be freed. Exactly
	// 2 sites: the inline-consumed result temp + the "x" literal operand (3
	// would mean the aliased identity result was mis-freed — the over-release
	// this case exists to catch); the exit code proves s survives the concat.
	{"tostring-string-recv-alias-safe",
		`function main(): i32 {
    var s: string = "abcd";
    if (("x" + s.to_string()).len() != 5) { return 97; }
    if (s.len() != 4) { return 96; }
    return 0;
}`,
		0, 2},
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
			reclaims := countUserStrFreeReclaims(asm)
			if tc.wantReclaims < 0 && reclaims == 0 {
				t.Errorf("%s: expected a fresh-operand reclaim (call __fn___fern_str_free), found none — the temp leaks", tc.name)
			}
			if tc.wantReclaims >= 0 && reclaims != tc.wantReclaims {
				t.Errorf("%s: expected exactly %d reclaim site(s), found %d — extra sites mean an aliased operand was mis-freed (over-release / UAF risk)", tc.name, tc.wantReclaims, reclaims)
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
