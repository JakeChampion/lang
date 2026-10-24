package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostEnumStrPayloadReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
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
			t.Errorf("%s exited %d, want %d (98 = heap bump grew → payload not reclaimed; 99 = over-release; 97 = value corrupted)", name, code, want)
		}
	}

	run(t, `enum Tok { Word(string), Num(i32) }
function go(pre: string): i32 { let x = Word(pre + "abc"); let r = 0; match (x) { Word(s) => { r = s.len(); }, Num(n) => { r = n; }, } return r; }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"enum-str-payload-flat", 0)

	run(t, `function go(pre: string): i32 { let o: Option[string] = Some(pre + "xyz"); let r = 0; match (o) { Some(s) => { r = s.len(); }, None => { r = 1; }, } return r; }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"option-str-payload-flat", 0)

	run(t, `function go(pre: string): i32 { let r2: Result[i32, string] = Err(pre + "e"); let r = 0; match (r2) { Ok(v) => { r = v; }, Err(e) => { r = e.len(); }, } return r; }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + go(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"result-err-str-payload-flat", 0)

	run(t, `function go(pre: string): i32 { let nm: string = pre + "q"; let o: Option[string] = Some(nm); let r = 0; match (o) { Some(s) => { r = s.len(); }, None => { r = 1; }, } return r + nm.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (go(pre) != 6) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"option-aliased-str-payload-excluded", 0)

	run(t, `enum Tok { Word(string), Num(i32) }
function go(pre: string): string { let x = Word(pre + "abc"); match (x) { Word(s) => { return s; }, Num(n) => { return "n"; }, } return ""; }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (go(pre).len() != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"enum-escaping-binding-return-moved", 0)

	run(t, `enum Tok { Word(string), Num(i32) }
function go(pre: string): i32 {
    let out: string = "";
    let x = Word(pre + "abc");
    match (x) { Word(s) => { out = s; }, Num(n) => { out = "n"; }, }
    return out.len();
}
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (go(pre) != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"enum-escaping-binding-store-moved", 0)

	run(t, `enum Tok { Word(string), Num(i32) }
function go(pre: string, k: i32): i32 {
    let out: string = "";
    let x = Word(pre + "abc");
    match (x) { Word(s) when k > 0 => { out = slice_unchecked(s, 0, 2) + ""; }, Word(s) => { out = s; }, Num(n) => { out = "n"; }, }
    return out.len();
}
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (go(pre, 1) != 2) { bad = 1; } if (go(pre, 0) != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"enum-guarded-move-mix-rejected", 0)

	run(t, `function go(pre: string, k: i32): i32 { let o: Option[string] = Some(pre + "x"); let t = 0; if (k < 2) { match (o) { Some(s) => { t = s.len(); }, None => { t = 0; }, } } return t; }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (go(pre, 1) != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"option-str-nested-if-sound", 0)
}
