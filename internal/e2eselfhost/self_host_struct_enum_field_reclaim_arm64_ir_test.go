package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStructEnumFieldReclaimIRArm64 is the arm64 port of the #4297 A2
// direct-enum-field exit-reclaim (x86 sibling: TestSelfHostStructEnumFieldReclaimIRX86_64).
// A struct carrying a direct enum field is admitted to the reclaim set, and the
// k_enum arm of `__struct_drop_<T>` SHALLOW-frees the enum box via __fern_arr_dec
// (one level — the variant payload leaks; churn keeps payloads scalar so the box
// free balances). Under qemu the reclaim is proven by CORRECTNESS (a wrong free of
// a live enum box corrupts the read-back match) plus an asm-shape assertion that
// `__struct_drop_Tagged` is emitted at all — which requires the gate (Site 2) to
// admit the enum-field struct. Heavy heap-exhaustion churn is left to the x86 path
// (too slow under qemu).
func TestSelfHostStructEnumFieldReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int, wantAsmSubstr string) {
		t.Helper()
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
		if len(asm) == 0 {
			t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", name)
		}
		if wantAsmSubstr != "" && !strings.Contains(string(asm), wantAsmSubstr) {
			t.Fatalf("%s: emitted arm64 asm missing %q — the enum-field struct was not admitted to the reclaim set", name, wantAsmSubstr)
		}
		bin := buildBinArm64(t, arm64gcc, dir, name, string(asm))
		cmd := runArm64Bin(qemu, bin)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	// SHAPE + VALUE: `Tagged { e: Shape, n: i32 }` has only a direct enum field (plus
	// a scalar), so it is reclaimable SOLELY via the new decl_is_enum gate clause —
	// asserting `__struct_drop_Tagged` is emitted proves the gate admitted it (and
	// thus the k_enum arm runs). `Rect(7)` is fresh (no construction inc); the enum
	// box is read back via match before the drop, so a wrong free would corrupt it.
	// Value: match on Rect(7) → 7 + n(5) = 12.
	run(t, `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { e: Shape, n: i32 }
function main(): i32 {
    var t: Tagged = Tagged { e: Rect(7), n: 5 };
    var r: i32 = 0;
    match (t.e) { Rect(v) => { r = v; }, _ => { r = 0; } }
    return r + t.n;
}`, "struct_enum_field_arm64_shape", 12, "__struct_drop_Tagged")

	// BALANCE UNDER CHURN: an aliased enum field (`e: s` from a live enum local) is
	// co-owned via the construction rc_inc; the k_enum drop decs the dup, `s` frees
	// at rc 0. A mis-balance would double-free and corrupt/crash under qemu. 200000
	// build/drop cycles staying correct (value 0) proves balance on the arm64 arm.
	run(t, `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { e: Shape, n: i32 }
function churn(n: i32): i32 {
    var bad: i32 = 0; var i: i32 = 0;
    while (i < n) {
        var s: Shape = Rect(9);
        var t: Tagged = Tagged { e: s, n: 1 };
        match (t.e) { Rect(v) => { if (v != 9) { bad = 1; } }, _ => { bad = 1; } }
        i = i + 1;
    }
    return bad;
}
function main(): i32 { return churn(200000); }`, "struct_enum_field_arm64_churn", 0, "")
}
