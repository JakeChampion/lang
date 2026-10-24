package e2e

import "testing"

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
    let total: i32 = 0;
    let k: i32 = 0;
    while (k < 200) { total = total + mk(); k = k + 1; }
    return total + __rc_underflow_count();
}`
	}
	const strDecl = `    let m: Map[string, i32] = map_new(8);
    m = m.insert("ke" + "y", 7);
    m = m.insert("ot" + "her", 3);`
	const intDecl = `    let m: Map[i32, i32] = map_new(8);
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
				{"string_key_hit", strDecl, `    let st_had: boolean = m.contains("ke" + "y"); let st = m.remove("ke" + "y");
    m = st;
    if (st_had) { }`},
				{"string_key_miss", strDecl, `    let st_had: boolean = m.contains("zz" + "zz"); let st = m.remove("zz" + "zz");
    m = st;
    if (st_had) { }`},
				{"i32_key_hit", intDecl, `    let st_had: boolean = m.contains(1); let st = m.remove(1);
    m = st;
    if (st_had) { }`},
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
