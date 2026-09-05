package e2e

import "testing"

// Deleting an entry orphaned its boxed KEY cell.
//
// `__map_delete_keyed_impl` freed the value cell, tombstoned the bucket and
// swapped the last entry into the hole — which overwrites the key slot, so the
// cell it pointed at became unreachable. `__drop_map_str_keys` only ever walks
// LIVE entries, so nothing ever returned it: 16 B per delete hit on the two
// boxing ABIs (#8493).
//
// The three rows are what identify it, and each kills a different explanation:
//
//   - a delete MISS is clean, so it is not the lookup box or anything about
//     probing;
//   - an i32 KEY is clean, so it is the key column and not the entry;
//   - x86-64 is clean either way, because it stores a string key as its data
//     pointer with no cell at all — which is also why this hid for so long.
//
// Absolute on all three backends. The surrounding map reclaims fully since
// #8276, so 16 bytes finally has somewhere to show up; before that it was
// buried under six figures of unrelated leak and unmeasurable.
func TestMapDeleteReclaimsTheKeyCell(t *testing.T) {
	src := func(decl, del string) string {
		return `
import "core/int";
import "core/map";
import "std/string";
function mk(): i32 {
` + decl + `
` + del + `
    return 0;
}
function main(): i32 {
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 200) { total = total + mk(); k = k + 1; }
    return total + __rc_underflow_count();
}`
	}
	const strDecl = `    var m: Map[string, i32] = map_new(8);
    m = m.insert("ke" + "y", 7);
    m = m.insert("ot" + "her", 3);`
	const intDecl = `    var m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 7);
    m = m.insert(2, 3);`

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
			for _, shape := range []struct{ name, decl, del string }{
				{"string_key_hit", strDecl, `    var st = m.without("ke" + "y");
    m = st.0;
    if (st.1) { }`},
				{"string_key_miss", strDecl, `    var st = m.without("zz" + "zz");
    m = st.0;
    if (st.1) { }`},
				{"i32_key_hit", intDecl, `    var st = m.without(1);
    m = st.0;
    if (st.1) { }`},
			} {
				t.Run(shape.name, func(t *testing.T) {
					_, stderr, code := tc.run(t, src(shape.decl, shape.del))
					if code != 0 {
						t.Fatalf("exit=%d, want 0 — a non-zero __rc_underflow_count() lands here", code)
					}
					allocs, frees, live := parseLeakCheckLine(t, stderr)
					if allocs == 0 {
						t.Fatalf("no allocations — the map is not being built")
					}
					if allocs != frees || live != 0 {
						t.Errorf("allocs=%d frees=%d live_bytes=%d, want balanced / 0 — "+
							"the deleted entry's boxed key cell is orphaned (#8493)",
							allocs, frees, live)
					}
				})
			}
		})
	}
}
