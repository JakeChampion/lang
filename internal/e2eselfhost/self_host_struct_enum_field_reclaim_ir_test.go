package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStructEnumFieldReclaimIRX86_64 pins the #4297 A2 slice: a DIRECT
// enum FIELD of a reclaimable, non-escaping struct local is now reclaimed when
// the struct is dropped. A struct carrying a direct enum field is admitted to the
// reclaim set (struct_has_reclaim_array_field's decl_is_enum clause), the struct-
// lit construction retains (rc_inc) a NON-fresh enum field (a fresh variant ctor
// `V(args)`/`V` is sole-owned and handed over with no inc), and the k_enum arm of
// the per-type __struct_drop SHALLOW-frees the enum box via __fern_arr_dec (one
// level — the variant payload leaks, matching the shallow k_struct gap; the churn
// cases keep payloads SCALAR so the box free fully balances).
//
// The reclaim is proven by SCALE: an enum-field struct is built and dropped every
// iteration. WITHOUT the k_enum arm the fresh enum box leaks each iteration and
// millions of iterations exhaust the heap (SIGKILL 137); WITH it the heap stays
// flat. A spurious double-free (mis-balanced construction inc) would instead tick
// __rc_underflow() -> exit 99. Exit 0 proves the enum field is reclaimed
// AND balanced (no over-release) over millions of build/drop cycles.
func TestSelfHostStructEnumFieldReclaimIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (137 = heap exhausted → enum field not reclaimed; 99 = over-release)", name, code, want)
		}
	}

	// GATE + ARM AT SCALE: struct `Tagged { e: Shape, n: i32 }` has ONLY a direct
	// enum field (plus a scalar), so it is reclaimable SOLELY via the new
	// struct_has_reclaim_array_field decl_is_enum clause — this exercises the gate
	// (Site 2) AND the k_enum arm (Site 4). `Rect(i)` is a FRESH variant ctor
	// (sole-owned rc=1, no construction inc) → the enum box is freed each iter; `t`
	// never escapes, so it is swept every iteration. The payload is a SCALAR, so
	// nothing leaks one level. 2,000,000 build/drop cycles stay flat → exit 0;
	// without the k_enum arm the fresh enum box leaks → SIGKILL (137).
	run(t, `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { e: Shape, n: i32 }
function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var t: Tagged = Tagged { e: Rect(i), n: i }; match (t.e) { Rect(v) => { if (v != i) { bad = 1; } }, _ => { bad = 1; } } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"struct-enum-field-fresh-gate-churn", 0)

	// NON-FRESH (aliased) enum field: `e` is bound from a live enum local `s`, so
	// the struct co-owns it via the construction rc_inc (Site 3a, non-variant-ctor
	// ident) and the k_enum drop only DECS the dup — `s` (swept at scope exit via
	// emit_enum_variant_drops) frees it at rc 0. Balanced: no over-release
	// (underflow 0) over 2,000,000 cycles, and no premature free (t.e still matches
	// while s is live). Exit 0; a mis-balanced inc/dec would tick underflow → 99.
	run(t, `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { e: Shape, n: i32 }
function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var s: Shape = Rect(7); var t: Tagged = Tagged { e: s, n: 1 }; match (t.e) { Rect(v) => { if (v != 7) { bad = 1; } }, _ => { bad = 1; } } match (s) { Rect(w) => { if (w != 7) { bad = 1; } }, _ => { bad = 1; } } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"struct-enum-field-aliased-balanced", 0)

	// FUNCTIONAL-UPDATE base-copy: `t2 = Tagged { ...t1, n: 2 }` copies `e` from t1
	// (un-overridden), so t2.e ALIASES t1.e. The base-copy retain (rc_inc, Site 3b)
	// lets t2's k_enum drop only DEC the dup; without it t2's drop would free t1's
	// enum box → over-release. Both t1 and t2 are reclaimable non-escaping locals
	// swept each iteration. Balanced across 2,000,000 cycles (underflow 0) with
	// t1.e still valid → exit 0; the pre-fix double-free would tick underflow → 99.
	run(t, `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { e: Shape, n: i32 }
function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var s: Shape = Rect(9); var t1: Tagged = Tagged { e: s, n: 1 }; var t2: Tagged = Tagged { ...t1, n: 2 }; match (t2.e) { Rect(v) => { if (v != 9) { bad = 1; } }, _ => { bad = 1; } } match (t1.e) { Rect(w) => { if (w != 9) { bad = 1; } }, _ => { bad = 1; } } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"struct-enum-field-base-copy-balanced", 0)

	// ENUM FIELD ALONGSIDE AN ARRAY FIELD: `Tagged { e: Shape, items: i32[] }` is
	// reclaimable via the array too, so __struct_drop is emitted regardless — this
	// proves the k_enum arm FIRES (frees the enum box) even when the gate would
	// admit the struct anyway. Fresh `Rect(i)` box + fresh `items` array both freed
	// each iter → flat over 2,000,000 cycles → exit 0; a leaked enum box → 137.
	run(t, `enum Shape { Circle, Square, Rect(i32) }
struct Tagged { e: Shape, items: i32[] }
function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var t: Tagged = Tagged { e: Rect(i), items: [1, 2, 3] }; if (t.items.len() != 3) { bad = 1; } match (t.e) { Rect(v) => { if (v != i) { bad = 1; } }, _ => { bad = 1; } } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000000); if (__rc_underflow() != 0) { return 99; } return v; }`,
		"struct-enum-field-with-array-churn", 0)
}
