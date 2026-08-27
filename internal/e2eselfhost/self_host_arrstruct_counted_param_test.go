package e2eselfhost

import (
	"testing"
)

// --- A struct-array local at a COUNTED-STORE argument position ----------------
//
// The struct-array twin of self_host_arrenum_counted_param_test.go, and the last
// of the five construction-retain `__param` cells. Same leak, same cause, same
// two guards:
//
//	callee stores `src` in a struct field   104 allocs / 102 frees, 88 live
//
// arrstruct_elem_esc_expr read every argument position it could not prove
// element-safe as an escape. A callee that STORES the array is not element-safe
// by the "ELB:" question — that flag asks whether the callee touches an ELEMENT,
// and one that stores the whole array touches none yet keeps a reference — so
// the caller's local lost its element walk and the sweep emitted the shallow
// __fern_arr_dec where emit_arrstruct_deep_free was owed.
//
// ONE TIER SERVES BOTH ELEMENT KINDS. "DCNT:" grew a struct arm rather than
// gaining a fifth key, because what the caller RELEASES is decided by its own
// local's type — emit_arrenum_deep_free or emit_arrstruct_deep_free — not by
// which type proved the store counted. The two escape walkers differ only in
// which deep free they route; the question they ask of the registry is
// identical, and borrow_reg_with_counted already merges every tier into one
// "CNT:" flag for exactly that reason.
//
// The walk-exists guard is asked with the predicate the RELEASE side asks:
// struct_has_reclaim_array_field, which slot_is_reclaimable_arrstruct checks
// before routing emit_arrstruct_deep_free. (The enum arm asks
// enum_arr_elems_walk_ok, what the backends check before emitting
// __enum_arr_elems_drop_<E>.) Crediting a walk nothing emits would leave the
// leak in place rather than close it.
//
// The element-handout guard needed no new code, as in the enum twin:
// arrparam_use_ok credits `p[i]` only for the STRING tier, so an element read
// disqualifies the param outright.
//
// Every want was confirmed against BOTH oracles — native x86-64 and
// `bin/fern -interp` agreed on each exit — and never read off the self-host run.
// All four were measured against the UNFIXED compiler first: only
// `counted_store` moved (104/102 -> 104/104) and the three refusals are
// byte-identical before and after.
//
// The local is built from `mkv(seed())`, never a literal: a bare-literal
// producer argument makes the local precise-drop eligible, which was its own
// defect (#7610) and reads exactly like this one.

const arrstructCountedDecl = `struct Inner { xs: i32[], k: i32 }
struct P { f: Inner[], n: i32 }
struct Q { e: Inner, n: i32 }
function mkv(i: i32): Inner[] { var o: Inner[] = []; o = o.append(Inner { xs: [i, i + 1], k: i }); return o; }
function seed(): i32 { return 7; }
`

func arrstructCountedMain(use string) string {
	return `
function main(): i32 {
    var keep: Inner[] = mkv(seed());
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + ` + use + `; r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`
}

func arrstructCountedCases() []arrenumShareCase {
	mk := func(decls, use string) string {
		return arrstructCountedDecl + decls + arrstructCountedMain(use)
	}
	return []arrenumShareCase{
		{
			// The repro: the callee stores the whole array in a struct literal
			// whose holder dies inside it, so the store's retain and the
			// holder's field drop net to zero and the caller's claim is the only
			// one left. 104/102 before, 104/104 now.
			name: "counted_store",
			src: mk(`function rd(src: Inner[], i: i32): i32 { var p: P = P { f: src, n: i }; return (p.f.len() + p.n) % 101; }`,
				"rd(keep, r)"),
			want: 6, balance: true,
		},
		{
			// REFUSED before the tier is consulted: an array RESULT can BE the
			// argument, and the caller's release fires immediately after the call.
			name: "callee_returns_param",
			src: mk(`function rd(src: Inner[], i: i32): Inner[] { return src; }`,
				"rd(keep, r).len()"),
			want: 3,
		},
		{
			// REFUSED by the use vocabulary: `src[0]` is an element read, and an
			// array element may BE a reference handed out uncounted.
			name: "callee_extracts_element",
			src: mk(`function rd(src: Inner[], i: i32): i32 { var e: Inner = src[0]; return e.xs.len() + i; }`,
				"rd(keep, r)"),
			want: 9,
		},
		{
			// What makes the element guard load-bearing rather than decorative:
			// the callee stores an ELEMENT, not the array. That store IS counted
			// for the element, so a tier asking only "is this a counted store?"
			// would admit it and the caller's walk would free a box the holder
			// still references. Stays refused.
			name: "callee_stores_element",
			src: mk(`function rd(src: Inner[], i: i32): i32 { var q: Q = Q { e: src[0], n: i }; return q.e.xs.len() + q.n; }`,
				"rd(keep, r)"),
			want: 9,
		},
	}
}

// TestSelfHostArrStructCountedParamX86_64 — a struct-array local handed to a
// callee that stores it at a COUNTED position keeps its element walk, while
// every callee that could let an element outlive the call keeps refusing it.
func TestSelfHostArrStructCountedParamX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrstructCountedCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrstructcounted_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 139 = it read freed memory)",
					tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}
