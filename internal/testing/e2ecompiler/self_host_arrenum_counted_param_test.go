package e2ecompiler

import (
	"testing"
)

// --- An enum-array local at a COUNTED-STORE argument position -----------------
//
// `rd(keep, r)` where `rd` STORES the param in a struct literal. The store is
// counted — the literal retains the array and the holder's field drop releases
// it — so the caller's own claim is untouched across the call and its release
// still owes the deep element walk, not the shallow __fern_arr_dec. Missed, the
// one element box and its i32[] payload leak (104 allocs / 102 frees).
//
// The refused cases pin the guards that keep this narrow: an array result can
// be the argument itself, and an element read or an element store hands a box
// out of the array. Each stays refused — a leak, never a free of a box something
// still references.
//
// Every want was confirmed against `bin/fern -interp`, never read off the
// self-host run.

const arrenumCountedDecl = `enum E { A(i32[]), B }
struct P { f: E[], n: i32 }
struct Q { e: E, n: i32 }
function mkv(i: i32): E[] { let o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }
function seed(): i32 { return 7; }
`

// arrenumCountedMain keeps `keep` genuinely live across the loop. The producer
// argument is `seed()`, never a literal, so the local is not const-folded dead
// (#7610).
func arrenumCountedMain(use string) string {
	return `
function main(): i32 {
    let keep: E[] = mkv(seed());
    let t: i32 = 0; let r: i32 = 0;
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
			src: mk(`function rd(src: E[], i: i32): i32 { let p: P = P { f: src, n: i }; return (p.f.len() + p.n) % 101; }`,
				"rd(keep, r)"),
			want: 6, balance: true,
		},
		{
			// REFUSED before the tier is consulted: an array RESULT can BE the
			// argument, and the caller's release fires immediately after the
			// call. The tier requires a concrete scalar result for exactly this.
			name: "callee_returns_param",
			src: mk(`@noinline function rd(src: E[], i: i32): E[] { return src; }`,
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
			src: mk(`function rd(src: E[], i: i32): i32 { let e: E = src[0]; return (match (e) { E.A(xs) => xs.len(), E.B => 0 }) + i; }`,
				"rd(keep, r)"),
			want: 9,
		},
		{
			// The case that makes the element guard essential rather than
			// decorative: the callee stores an ELEMENT — not the array — in a
			// struct field. The store is counted for the ELEMENT, so a tier
			// that asked only "is this a counted store?" would admit it, and
			// the caller's element walk would then free a box the holder still
			// references. Stays refused.
			name: "callee_stores_element",
			src: mk(`function rd(src: E[], i: i32): i32 { let q: Q = Q { e: src[0], n: i }; return (match (q.e) { E.A(xs) => xs.len(), E.B => 0 }) + q.n; }`,
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
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
