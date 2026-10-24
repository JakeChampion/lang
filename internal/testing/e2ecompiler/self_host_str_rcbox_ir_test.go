package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"
)

func TestSelfHostStrRcBoxIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	run := func(t *testing.T, prog, name string, want int, wantBox bool) {
		t.Helper()
		asm := []byte(l.emit(t, prog))
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

	run(t, `function churn(n: i32): i32 { let pre: string = "aa"; let suf: string = "bb"; let t: i32 = 0; let i: i32 = 0; while (i < n) { let r: string = pre + "x" + suf; if (r.len() < 5) { t = 1; } i = i + 1; } return t; } function main(): i32 { let v: i32 = churn(2000000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"rcbox-churn-no-underflow", 0, true)

	run(t, `@noinline function ids(s: string): string { return s; } function mk(): i32 { let s: string = "!" + ids("hi"); return s.len(); } function main(): i32 { let r: i32 = mk(); if (__rc_underflow_count() != 0) { return 99; } return r; }`,
		"rcbox-scope-exit", 3, true)

	run(t, `import "std/i32";
function churn(n: i32): i32 { let ok: i32 = 0; let i: i32 = 0; while (i < n) { let s: string = i.to_string(); if (s.len() < 1) { ok = 1; } i = i + 1; } return ok; } function main(): i32 { let r: i32 = churn(2000000); if (__rc_underflow_count() != 0) { return 99; } return r; }`,
		"rcbox-i32-to-string-churn", 0, true)
}
