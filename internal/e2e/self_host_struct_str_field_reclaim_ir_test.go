package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStructStrFieldReclaimIRX86_64 pins the #4297 A2 slice: a `string`
// FIELD of a reclaimable, non-escaping struct local is now reclaimed when the
// struct is dropped. The struct-lit construction retains (rc_inc) a non-fresh
// string field — gated on the per-lit ownership precompute (field_ownerships /
// str_producer_ownership) so the classifying read stays out of lower_expr's hot
// path — and the k_str arm of the per-type __struct_drop frees it (rc-aware:
// free at rc==1, dec at rc>1, skip an immortal view/literal at rc<0).
//
// The reclaim is proven by SCALE: a fresh-string-field struct is built and
// dropped every iteration. WITHOUT the field-drop the fresh name box leaks each
// iteration and millions of iterations exhaust the heap (SIGKILL 137); WITH it
// the heap stays flat. A spurious double-free would instead tick
// __fern_rc_underflow_count() -> exit 99. Exit 0 proves the field is reclaimed
// AND balanced (no over-release) over millions of build/drop cycles.
func TestSelfHostStructStrFieldReclaimIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (137 = heap exhausted → field not reclaimed; 99 = over-release)", name, code, want)
		}
	}

	// RECLAIM AT SCALE: struct `R { name: string, items: i32[] }` is reclaimable
	// (has an rc-array field), so its exit-sweep drop deep-drops `items` AND now
	// frees `name`. `name` is a FRESH concat (sole-owned rc=1) → freed each iter;
	// `r` never escapes, so it's swept every iteration. 2,000,000 build/drop
	// cycles stay flat (name freed) → exit 0; a leak would SIGKILL (137).
	run(t, `struct R { name: string, items: i32[] }
function churn(n: i32): i32 { var pre: string = "aa"; var bad: i32 = 0; var i: i32 = 0; while (i < n) { var r: R = R { name: pre + "x", items: [1, 2, 3] }; if (r.name.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000000); if (__fern_rc_underflow_count() != 0) { return 99; } return v; }`,
		"struct-str-field-reclaim-churn", 0)

	// NON-FRESH (aliased) string field: `name` is bound from a live local `nm`,
	// so the struct co-owns it via the construction rc_inc and the field-drop only
	// DECS the dup — `nm` (swept at scope exit) frees it at rc 0. Balanced: no
	// over-release (underflow 0) over 2,000,000 cycles, and no premature free
	// (r.name reads len 3 while nm is still live). Exit 0.
	run(t, `struct R { name: string, items: i32[] }
function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var nm: string = "abc"; var r: R = R { name: nm, items: [1] }; if (r.name.len() != 3) { bad = 1; } if (nm.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000000); if (__fern_rc_underflow_count() != 0) { return 99; } return v; }`,
		"struct-str-field-aliased-balanced", 0)

	// FUNCTIONAL-UPDATE base-copy: `r2 = R { ...r1, items: [...] }` copies `name`
	// from r1 (un-overridden), so r2.name ALIASES r1.name. The base-copy retain
	// (rc_inc, gated on the struct being reclaimable) lets r2's field-drop only DEC
	// the dup; without it r2's drop would free r1's name → over-release. Both r1 and
	// r2 are reclaimable non-escaping locals swept each iteration. Balanced across
	// 2,000,000 cycles (underflow 0) with r1.name still valid (len 3) → exit 0;
	// the pre-fix double-free would tick the underflow counter → exit 99.
	run(t, `struct R { name: string, items: i32[] }
function churn(n: i32): i32 { var bad: i32 = 0; var i: i32 = 0; while (i < n) { var nm: string = "abc"; var r1: R = R { name: nm, items: [1] }; var r2: R = R { ...r1, items: [2, 3] }; if (r2.name.len() != 3) { bad = 1; } if (r1.name.len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { var v: i32 = churn(2000000); if (__fern_rc_underflow_count() != 0) { return 99; } return v; }`,
		"struct-str-field-base-copy-balanced", 0)
}
