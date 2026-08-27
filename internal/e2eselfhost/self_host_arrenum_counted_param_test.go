package e2eselfhost

import (
	"testing"
)

// --- An enum-array local at a COUNTED-STORE argument position -----------------
//
// `rd(keep, r)` where `rd` STORES the param in a struct literal. The arrenum
// escape gate read every argument position it could not prove element-safe as
// an escape, and a callee that keeps the array is not element-safe by the
// "ELB:" question — so the caller lost its element walk and leaked a constant
// two objects (the one element box and its i32[] payload) however many times
// the callee ran:
//
//	callee stores `src` in a struct field   104 allocs / 102 frees, 80 live
//
// The buffer itself was freed: the sweep emitted the SHALLOW __fern_arr_dec
// where the deep element walk was owed. That is what separates this from the
// borrowed-argument slice next door, where the release was withheld entirely.
//
// THE ELEMENT FLAG IS THE WRONG QUESTION HERE, and it is why this needed a
// second tier rather than a widening of "ELB:". That flag asks whether the
// callee touches an element; a callee that stores the whole array touches none,
// yet keeps a reference, so the flag refuses. The reference it keeps is a
// COUNTED one — the struct literal incs the array (`is_array_type_name` covers
// `E[]` in the ExprStructLit arm) and the holder's field drop decs it — so the
// caller's own claim is untouched across the call and its walk is still owed.
// param_counted_of's "DCNT:" tier proves that, and borrow_reg_with_counted
// already publishes every counted tier to this walker under "CNT:"; the enum
// and array-of-boxes types were simply never admitted, because that registry
// admits BY TYPE.
//
// Two guards make the tier narrow rather than a blanket accept, and the refused
// cases below are what prove them load-bearing:
//
//   - The element walk must EXIST. enum_arr_elems_walk_ok is the same predicate
//     the backends ask before emitting __enum_arr_elems_drop_<E> at the arr_dec
//     site; crediting a walk nothing emits would leave the leak in place.
//   - No element may be handed out. arrparam_use_ok credits `p[i]` only for the
//     STRING tier, so any element read disqualifies the param outright — which
//     is the same hazard "ELB:" exists for, answered by the vocabulary instead
//     of by a second flag.
//
// Every want was confirmed against BOTH oracles — `bin/fern -interp` and the
// native x86-64 backend agreed on each exit — and never read off the self-host
// run. All four cases were measured against the UNFIXED compiler first: only
// `counted_store` moved (104/102 -> 104/104), and the three refusals are
// byte-identical before and after, so none of them passes for a reason
// unrelated to this tier.
//
// THE CONSTRUCTION-RETAIN MATRIX CELL DOES NOT MOVE, and that is not an
// oversight. `enum_arr__param` builds its local from `mkv(7)` — a CONSTANT
// producer argument — which makes the local dead and moves its release to a
// precise box-only site. That is the const-fold trap the borrowed-arg suite
// documents in its own header (it cites #7364 for it, but that issue is closed
// and is a different defect; the trap itself is #7610) — a second and
// independent cause stacked on this one: with this fix the constant shape still
// measures 104/102 while the identical program over `mkv(seed())` is clean. The
// cell needs both.
//
// The struct-array twin (`Inner[]`) is untouched: it has its own escape walker
// and its own tier, and follows as its own slice the way the arrstruct and
// arrenum halves of every earlier slice did.

const arrenumCountedDecl = `enum E { A(i32[]), B }
struct P { f: E[], n: i32 }
struct Q { e: E, n: i32 }
function mkv(i: i32): E[] { var o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }
function seed(): i32 { return 7; }
`

// arrenumCountedMain keeps `keep` genuinely live across the loop. The producer
// argument is `seed()`, never a literal: a constant one const-folds the local
// dead and leaks for the unrelated reason above, which reads exactly like this
// bug.
func arrenumCountedMain(use string) string {
	return `
function main(): i32 {
    var keep: E[] = mkv(seed());
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + ` + use + `; r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`
}

func arrenumCountedCases() []arrenumShareCase {
	mk := func(decls, use string) string {
		return arrenumCountedDecl + decls + arrenumCountedMain(use)
	}
	return []arrenumShareCase{
		{
			// The repro. The callee stores the whole array in a struct literal
			// whose holder dies inside the callee, so the store's retain and
			// the holder's field drop net to zero and the caller's claim is the
			// only one left. 104/102 before, 104/104 now.
			name: "counted_store",
			src: mk(`function rd(src: E[], i: i32): i32 { var p: P = P { f: src, n: i }; return (p.f.len() + p.n) % 101; }`,
				"rd(keep, r)"),
			want: 6, balance: true,
		},
		{
			// REFUSED before the tier is consulted: an array RESULT can BE the
			// argument, and the caller's release fires immediately after the
			// call. The tier requires a concrete scalar result for exactly this.
			name: "callee_returns_param",
			src: mk(`function rd(src: E[], i: i32): E[] { return src; }`,
				"rd(keep, r).len()"),
			want: 3,
		},
		{
			// REFUSED by the use vocabulary: `src[0]` is an element read, and
			// an array element may BE a reference handed out uncounted. Sound
			// to admit in this exact shape — the extracted box dies inside the
			// callee — but the floor is a leak either way and widening it needs
			// the arm-binding analysis.
			name: "callee_extracts_element",
			src: mk(`function rd(src: E[], i: i32): i32 { var e: E = src[0]; return (match (e) { E.A(xs) => xs.len(), E.B => 0 }) + i; }`,
				"rd(keep, r)"),
			want: 9,
		},
		{
			// The case that makes the element guard load-bearing rather than
			// decorative: the callee stores an ELEMENT — not the array — in a
			// struct field. The store is counted for the ELEMENT, so a tier
			// that asked only "is this a counted store?" would admit it, and
			// the caller's element walk would then free a box the holder still
			// references. Stays refused.
			name: "callee_stores_element",
			src: mk(`function rd(src: E[], i: i32): i32 { var q: Q = Q { e: src[0], n: i }; return (match (q.e) { E.A(xs) => xs.len(), E.B => 0 }) + q.n; }`,
				"rd(keep, r)"),
			want: 9,
		},
	}
}

// TestSelfHostArrEnumCountedParamX86_64 — an enum-array local handed to a callee
// that stores it at a COUNTED position keeps its element walk, while every
// callee that could let an element outlive the call keeps refusing it.
func TestSelfHostArrEnumCountedParamX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrenumCountedCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrenumcounted_"+tc.name, asm)
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
