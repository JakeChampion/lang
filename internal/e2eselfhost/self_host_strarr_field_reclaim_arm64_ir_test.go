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
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
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
}
