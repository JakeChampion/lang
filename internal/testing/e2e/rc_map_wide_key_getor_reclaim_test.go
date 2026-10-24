package e2e

import "testing"

func TestMapWideKeyGetOrFallbackIsReclaimed(t *testing.T) {
	src := func(read string) string {
		return `
import "core/int";
import "core/map";
function mk(): i32 {
    let m: Map[i64, i32[]] = map_new(8);
    m = m.insert(7 as i64, [1, 2]);
` + read + `
}
function main(): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 100) { t = t + mk(); k = k + 1; }
    return 0;
}`
	}
	const insertOnly = `    return 0;`
	const freshFallback = `    let v: i32[] = m.get_or(9 as i64, [0]);
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
