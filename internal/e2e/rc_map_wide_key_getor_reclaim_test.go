package e2e

import "testing"

// #8432's fix is not string-key-specific, and the log that landed with it
// framed it as though it were.
//
// The counted-read `get_or` arm is gated on `needBoxK`, which is
// `isStringForBoxing(K) || mapKeyKindTag(K) == 2` — a two-word string key OR a
// WIDE SCALAR key, which wasm32 boxes because an i64 does not fit its 4-byte
// slot. Both were losing the fallback's release; only the string half had a
// shape watching it.
//
// Pinned as a DIFFERENTIAL rather than an absolute census, because wasm32
// leaks the boxed wide-KEY cell that `insert` stores whichever way this goes —
// 16 B an iteration, present with no read in the program at all (the wide-key
// column walk, #8276 / #8171's neighbour). Subtracting the insert-only
// baseline measures the read and nothing else, so this stays honest while that
// gap is open and does not silently start passing if it closes.
//
// wasm32, 100 rounds: insert-only 1600, with the read 4800 before the fix and
// 1600 after — the fallback array was the whole difference. arm64 and x86-64
// hold an i64 key in a pointer slot, so they never box it and read 0 either
// way; they ride along to prove the shape is not wasm-only by construction.
func TestMapWideKeyGetOrFallbackIsReclaimed(t *testing.T) {
	src := func(read string) string {
		return `
import "core/int";
import "core/map";
function mk(): i32 {
    var m: Map[i64, i32[]] = map_new(8);
    m = m.insert(7 as i64, [1, 2]);
` + read + `
}
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 100) { t = t + mk(); k = k + 1; }
    return 0;
}`
	}
	const insertOnly = `    return 0;`
	const freshFallback = `    var v: i32[] = m.get_or(9 as i64, [0]);
    return v.len();`

	for _, tc := range []struct {
		name string
		run  func(*testing.T, string) (string, string, int)
	}{
		{"x86_64", runLeakCheckX86_64},
		{"arm64", runLeakCheckArm64},
		{"wasm", func(t *testing.T, src string) (string, string, int) {
			return runLeakCheckWasm(t, src, false)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := func(body string) int64 {
				t.Helper()
				_, stderr, code := tc.run(t, src(body))
				if code != 0 {
					t.Fatalf("exit=%d, want 0", code)
				}
				allocs, _, l := parseLeakCheckLine(t, stderr)
				if allocs == 0 {
					t.Fatalf("no allocations — the map is not being built")
				}
				return l
			}
			base := live(insertOnly)
			withRead := live(freshFallback)
			if withRead != base {
				t.Errorf("a get_or with a fresh fallback adds %d live bytes over the insert-only baseline "+
					"(%d vs %d), want 0 — the boxed WIDE key is switching the fallback's release off, "+
					"which is #8432 on the key kind that had no shape watching it",
					withRead-base, withRead, base)
			}
		})
	}
}
