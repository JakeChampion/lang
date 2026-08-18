package e2eselfhost

import (
	"strings"
	"testing"
)

// TestSelfHostStrArrFieldReclaimIRArm64 is the arm64 port of the string[]
// struct-field exit-reclaim (x86 sibling: the strarr-field-* cases in
// TestSelfHostStructStrFieldReclaimIRX86_64): a struct whose string[] field is
// admitted by the strarrfld scan ("strfldok:arr:<T>" — element-fresh stores,
// .len()-only reads) deep-frees the field via __fern_str_arr_free from both
// the __field_reclaim and __struct_drop arm64 bodies. Under qemu the reclaim
// is proven by CORRECTNESS + the underflow detector plus an asm-shape
// assertion that the deep-free call is emitted at all; heavy heap churn stays
// on the x86 leg.
//
// It also pins the x10-staleness fix in emit_arm64_struct_drop_one's k_str
// arm: __fern_str_free clobbers x10 (freelist-head scratch), and before the
// fix a `string` field ordered BEFORE another rc field left x10 stale, so the
// NEXT field's arm freed through a garbage box pointer.
func TestSelfHostStrArrFieldReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int, wantAsmSubstr string) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64-linux")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		if wantAsmSubstr != "" && !strings.Contains(string(asm), wantAsmSubstr) {
			t.Fatalf("%s: emitted arm64 asm missing %q — the string[]-field struct was not admitted / the arm not emitted", name, wantAsmSubstr)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	// ADMITTED string[] field: element-fresh literal stores, .len()-only reads.
	// 300k build/drop cycles under qemu — balanced (underflow 0), values right,
	// and the deep-free call present in the emitted asm.
	run(t, `struct Diag { code: i32, notes: string[] }
function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var d: Diag = Diag { code: i, notes: ["alpha", "beta" + "x"] }; if (d.notes.len() != 2) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(300000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-field-reclaim-arm64", 0, "bl __fn___fern_str_arr_free")

	// STRING-BEFORE-ARRAY field order (the x10-staleness regression): R's
	// `name` (k_str, frees via __fern_str_free which clobbers x10) precedes
	// `items` (k_scalar, reads the box through x10). Pre-fix the items arm
	// freed through a stale x10 — a garbage dec that corrupts or ticks the
	// underflow detector at scale. 300k cycles balanced → exit 0.
	run(t, `struct R { name: string, items: i32[] }
function churn(n: i32): i32 { var pre: string = "aa"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { var r: R = R { name: pre + "x", items: [1, 2, 3] }; if (r.name.len() != 3) { bad = 1; } if (r.items.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(300000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"str-before-array-field-order-arm64", 0, "")

	// PRODUCER-CALL ELEMENTS: the field is built from calls to a proven
	// fresh-string producer rather than inline concats, which the store gate
	// now admits (strarr_value_is_fresh, the same question the "SARR:" credit
	// asks). Correctness + over-release under qemu; the x86 sibling carries the
	// flatness leg. 2 + 43 = 45 each build.
	run(t, `struct Diag { code: i32, notes: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function build(pre: string): i32 { var d: Diag = Diag { code: 1, notes: [w(pre), w(pre)] }; return d.notes.len() + d.notes.len() + 41; }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 45) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-field-producer-elements-arm64", 0, "bl __fn___fern_str_arr_free")

	// A SIBLING TYPE'S IDENTICALLY-NAMED FIELD, stored from a borrowed
	// parameter, is correctly refused — but the mark used to be the bare field
	// NAME and took `Diag.notes` with it. Keyed "<T>.<field>" now: Diag keeps
	// its deep free while Esc keeps the sound leak, and the caller's array
	// stays valid past the struct's drop. 2 + 43 + 43 + 1 = 89.
	run(t, `struct Esc { notes: string[] }
struct Ok { notes: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function fill(n: i32): string { var s: string = ""; var i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function mkesc(notes: string[]): Esc { return Esc { notes: notes }; }
function build(pre: string): i32 { var live: string[] = [w(pre), w(pre)]; var e: Esc = mkesc(live); var o: Ok = Ok { notes: [w(pre)] }; var junk: string = fill(20); if (junk.len() < 0) { return 0; } return e.notes.len() + live[0].len() + live[1].len() + o.notes.len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(1000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-field-sibling-name-arm64", 0, "")

	// WHOLE-ARRAY PRODUCER CALL as the field value: the store gate took only an
	// array LITERAL, so this shape was refused and leaked every element box.
	// It now asks fn_returns_fresh_strarr — the "STRARR:" registry's own rule.
	// Correctness + over-release under qemu; the x86 sibling carries flatness.
	// 3 + 43 = 46 each build.
	run(t, `struct Node { name: string, deps: string[], mtime: i32 }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function deps_of(pre: string): string[] { var out: string[] = []; var i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function build(pre: string): i32 { var f: Node = Node { name: w(pre), deps: deps_of(pre), mtime: 1 }; return f.deps.len() + f.name.len(); }
function churn(n: i32): i32 { var pre: string = "ab"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { if (build(pre) != 46) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"strarr-field-producer-call-store-arm64", 0, "bl __fn___fern_str_arr_free")
}
