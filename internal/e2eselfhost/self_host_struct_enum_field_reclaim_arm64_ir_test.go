package e2eselfhost

import (
	"testing"
)

// TestSelfHostStructEnumFieldReclaimIRArm64 is the arm64 port of the #4297 A2
// direct-enum-field exit-reclaim (x86 sibling: TestSelfHostStructEnumFieldReclaimIRX86_64).
// A struct carrying a direct enum field is admitted to the reclaim set, and the
// k_enum arm of `__struct_drop_<T>` SHALLOW-frees the enum box via __fern_arr_dec
// (one level — the variant payload leaks; churn keeps payloads scalar so the box
// free balances). Under qemu the reclaim is proven by CORRECTNESS (a wrong free of
// a live enum box corrupts the read-back match) plus a balanced arm64 census.
// Heavy heap-exhaustion churn is left to the x86 path (too slow under qemu).
func TestSelfHostStructEnumFieldReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		if code := arm64CensusRun(t, x86runner, driverBin, arm64gcc, qemu, name, prog); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	// `Tagged { e: Shape, n: i32 }` has only a direct enum field (plus a
	// scalar). `Rect(7)` is fresh (no construction inc); the enum
	// box is read back via match before the drop, so a wrong free would corrupt it.
	// Value: match on Rect(7) → 7 + n(5) = 12.
	// Both probes use runtime payloads so the census exercises heap reclamation.
	run(t, `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { e: Shape, n: i32 }
@noinline function runtime(n: i32): i32 { return n; }
function main(): i32 {
    let t: Tagged = Tagged { e: Rect(runtime(7)), n: 5 };
    let r: i32 = 0;
    match (t.e) { Rect(v) => { r = v; }, _ => { r = 0; } }
    return r + t.n;
}`, "struct_enum_field_arm64_shape", 12)

	// BALANCE UNDER CHURN: an aliased enum field (`e: s` from a live enum local) is
	// co-owned via the construction rc_inc; the k_enum drop decs the dup, `s` frees
	// at rc 0. A mis-balance would double-free and corrupt/crash under qemu. 20000
	// build/drop cycles staying correct (value 0) proves balance on the arm64 arm;
	// each cycle allocates, so a longer churn only costs qemu time.
	run(t, `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { e: Shape, n: i32 }
@noinline function runtime(n: i32): i32 { return n; }
function churn(n: i32): i32 {
    let bad: i32 = 0; let i: i32 = 0;
    while (i < n) {
        let s: Shape = Rect(runtime(9));
        let t: Tagged = Tagged { e: s, n: 1 };
        match (t.e) { Rect(v) => { if (v != 9) { bad = 1; } }, _ => { bad = 1; } }
        i = i + 1;
    }
    return bad;
}
function main(): i32 { return churn(20000); }`, "struct_enum_field_arm64_churn", 0)
}
