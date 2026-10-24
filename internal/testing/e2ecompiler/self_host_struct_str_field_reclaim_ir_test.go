package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostStructStrFieldReclaimIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (137 = heap exhausted → field not reclaimed; 99 = over-release)", name, code, want)
		}
	}

	run(t, `struct R { name: string, items: i32[] }
function churn(n: i32): i32 { let pre: string = "aa"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { let r: R = R { name: pre + "x", items: [1, 2, 3] }; if (r.name.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"struct-str-field-reclaim-churn", 0)

	run(t, `struct R { name: string, items: i32[] }
@noinline function ids(s: string): string { return s; }
function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let nm: string = ids("ab") + "c"; let r: R = R { name: nm, items: [1] }; if (r.name.len() != 3) { bad = 1; } if (nm.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"struct-str-field-aliased-balanced", 0)

	run(t, `struct R { name: string, items: i32[] }
@noinline function ids(s: string): string { return s; }
function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let nm: string = ids("ab") + "c"; let r1: R = R { name: nm, items: [1] }; let r2: R = R { ...r1, items: [2, 3] }; if (r2.name.len() != 3) { bad = 1; } if (r1.name.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"struct-str-field-base-copy-balanced", 0)

	run(t, `struct B { name: string }
struct A { inner: B, items: i32[] }
function churn(n: i32): i32 { let pre: string = "z"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { let a: A = A { inner: B { name: pre + "xy" }, items: [1, 2] }; if (a.inner.name.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(1500000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"nested-string-only-struct-reclaim", 0)

	run(t, `struct Diag { code: i32, notes: string[] }
@noinline function ids(s: string): string { return s; }
function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let d: Diag = Diag { code: i, notes: ["alpha", ids("beta") + "x"] }; if (d.notes.len() != 2) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(4000000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-field-reclaim-churn", 0)

	run(t, `struct Diag { code: i32, notes: string[] }
@noinline function ids(s: string): string { return s; }
function main(): i32 { let xs: string[] = [ids("a") + "b", "cd"]; let d: Diag = Diag { code: 3, notes: xs }; let s: i32 = d.code + xs[0].len() + xs.len(); if (s != 7) { return 90; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`,
		"strarr-field-aliased-excluded", 0)

	run(t, `struct Diag { code: i32, notes: string[] }
@noinline function ids(s: string): string { return s; }
function main(): i32 { let d: Diag = Diag { code: 3, notes: [ids("al") + "pha", "beta"] }; let n0: string = d.notes[0]; let s: i32 = d.code + d.notes.len() + n0.len(); if (s != 10) { return 90; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`,
		"strarr-field-read-excluded", 0)

	run(t, `struct Diag { code: i32, notes: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function build(pre: string): i32 { let d: Diag = Diag { code: 1, notes: [w(pre), w(pre)] }; return d.notes.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w0: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-field-producer-elements-flat", 0)

	run(t, `struct Diag { code: i32, notes: string[] }
struct Other { notes: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mkother(notes: string[]): Other { return Other { notes: notes }; }
function useother(pre: string): i32 { let o: Other = mkother([pre]); return o.notes.len(); }
function build(pre: string): i32 { let d: Diag = Diag { code: 1, notes: [w(pre), w(pre)] }; return d.notes.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let seed: i32 = useother("q"); if (seed < 0) { return 96; } let w0: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-field-sibling-name-not-poisoned", 0)

	run(t, `struct Esc { notes: string[] }
struct Ok { notes: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function fill(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function mkesc(notes: string[]): Esc { return Esc { notes: notes }; }
function build(pre: string): i32 { let live: string[] = [w(pre), w(pre)]; let e: Esc = mkesc(live); let o: Ok = Ok { notes: [w(pre)] }; let junk: string = fill(20); if (junk.len() < 0) { return 0; } return e.notes.len() + live[0].len() + live[1].len() + o.notes.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(3000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-field-borrowed-param-retained", 0)

	run(t, `struct Node { name: string, deps: string[], mtime: i32 }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function build(pre: string): i32 { let f: Node = Node { name: w(pre), deps: deps_of(pre), mtime: 1 }; return f.deps.len() + f.name.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w0: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-field-producer-call-store-flat", 0)

	run(t, `struct Sh { deps: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function fill(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function build(pre: string): i32 { let deps_of: string[] = [w(pre)]; let o: Sh = Sh { deps: deps_of }; let j: string = fill(20); if (j.len() < 0) { return 0; } return o.deps.len() + deps_of[0].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 44) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(3000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-field-producer-name-shadowed-retained", 0)
}
