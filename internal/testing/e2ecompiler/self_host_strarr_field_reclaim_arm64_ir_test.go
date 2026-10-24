package e2ecompiler

import (
	"testing"
)

func TestSelfHostStrArrFieldReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		if code := arm64CensusRun(t, x86runner, driverBin, arm64gcc, qemu, name, prog); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	run(t, `struct Diag { code: i32, notes: string[] }
function churn(n: i32): i32 { let bad: i32 = 0; let i: i32 = 0; while (i < n) { let d: Diag = Diag { code: i, notes: ["alpha", "beta" + "x"] }; if (d.notes.len() != 2) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(300000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-field-reclaim-arm64", 0)

	run(t, `struct R { name: string, items: i32[] }
function churn(n: i32): i32 { let pre: string = "aa"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { let r: R = R { name: pre + "x", items: [1, 2, 3] }; if (r.name.len() != 3) { bad = 1; } if (r.items.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(300000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"str-before-array-field-order-arm64", 0)

	run(t, `struct Diag { code: i32, notes: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function build(pre: string): i32 { let d: Diag = Diag { code: 1, notes: [w(pre), w(pre)] }; return d.notes.len() + d.notes.len() + 41; }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 45) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-field-producer-elements-arm64", 0)

	run(t, `struct Esc { notes: string[] }
struct Ok { notes: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function fill(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function mkesc(notes: string[]): Esc { return Esc { notes: notes }; }
function build(pre: string): i32 { let live: string[] = [w(pre), w(pre)]; let e: Esc = mkesc(live); let o: Ok = Ok { notes: [w(pre)] }; let junk: string = fill(20); if (junk.len() < 0) { return 0; } return e.notes.len() + live[0].len() + live[1].len() + o.notes.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(1000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-field-sibling-name-arm64", 0)

	run(t, `struct Node { name: string, deps: string[], mtime: i32 }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function build(pre: string): i32 { let f: Node = Node { name: w(pre), deps: deps_of(pre), mtime: 1 }; return f.deps.len() + f.name.len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 46) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-field-producer-call-store-arm64", 0)
}
