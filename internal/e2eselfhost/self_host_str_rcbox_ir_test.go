package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStrRcBoxIRX86_64 pins the #2649 Option-A change: asm heap string boxes
// are now rc-HEADERED. Every reclaimable box (a string literal via const_str, and every
// fresh producer via the raw_string op — concat / chr / i32_to_string / str_to_* / …) is
// built by the centralized __fern_str_box helper as [rc=1][data][len] (box = base+8, so
// data@0 / len@8 are unchanged), and __fern_str_free is now rc-aware: it decrements the
// rc, frees the data buffer + the 24-byte box only at rc==1, and a decrement below 1
// bumps the over-release counter instead of corrupting the heap (matching the array path
// and the wasm $__fern_str_box model).
//
// Two contracts:
//   - EMISSION: the driver emits `call __fern_str_box` (rc-headered construction) — the
//     old raw `movq $16 / __fern_alloc / store data / store len` box is gone.
//   - DOUBLE-FREE SAFETY NET: a long fresh-concat churn reclaims a box each iteration; a
//     spurious double-free would tick __rc_underflow(), which main checks and
//     maps to exit 99. Exit 0 proves the rc-aware free balances (flat heap, no
//     over-release) over millions of allocate/reclaim cycles.
func TestSelfHostStrRcBoxIRX86_64(t *testing.T) {
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

	run := func(t *testing.T, prog, name string, want int, wantBox bool) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		if wantBox && !bytes.Contains(asm, []byte("call __fern_str_box")) {
			t.Errorf("%s: expected `call __fern_str_box` (rc-headered box construction), found none", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (99 = over-release detected by the rc-aware __fern_str_free)", name, code, want)
		}
	}

	// DOUBLE-FREE SAFETY NET at scale: 2,000,000 fresh concat chains, each reclaimed. A
	// double-free would bump __rc_underflow() -> exit 99; a leak would exhaust
	// the heap (SIGKILL 137). r = "aaxbb" len 5, t stays 0, underflow 0 -> exit 0.
	run(t, `function churn(n: i32): i32 { var pre: string = "aa"; var suf: string = "bb"; var t: i32 = 0; var i: i32 = 0; while (i < n) { var r: string = pre + "x" + suf; if (r.len() < 5) { t = 1; } i = i + 1; } return t; } function main(): i32 { var v: i32 = churn(2000000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"rcbox-churn-no-underflow", 0, true)

	// VALUE + underflow check on a fresh literal-operand concat reclaimed at scope exit.
	// "hi"+"!" = len 3; underflow 0.
	run(t, `function mk(): i32 { var s: string = "hi" + "!"; return s.len(); } function main(): i32 { var r: i32 = mk(); if (__rc_underflow() != 0) { return 99; } return r; }`,
		"rcbox-scope-exit", 3, true)

	// i32_to_string churn: the digit-string box is rc-headered too; reclaimed each iter,
	// no over-release. Every decimal string has len >= 1, so ok stays 0 -> exit 0.
	run(t, `function churn(n: i32): i32 { var ok: i32 = 0; var i: i32 = 0; while (i < n) { var s: string = i32_to_string(i); if (s.len() < 1) { ok = 1; } i = i + 1; } return ok; } function main(): i32 { var r: i32 = churn(2000000); if (__rc_underflow() != 0) { return 99; } return r; }`,
		"rcbox-i32-to-string-churn", 0, true)
}
