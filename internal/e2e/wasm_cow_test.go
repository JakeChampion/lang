package e2e

import "testing"

// TestWASMAliasedArraySetCoW: a mutation through one alias of a shared array
// or map copies rather than writing through, so the other alias keeps its
// value. The heap here starts at ~1024, which is where an rc helper with a
// low-address guard skipped its increment and let `ys.with(...)` take the
// rc==1 in-place path on a shared buffer.
func TestWASMAliasedArraySetCoW(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"array_set", `function main(): i32 {
    let xs: i32[] = [10, 20, 30];
    let ys = xs;
    ys = ys.with(0, 999);
    if (xs[0] != 10) { return 1; }
    if (ys[0] != 999) { return 2; }
    return 0;
}`},
		{"array_index_assign", `function main(): i32 {
    let xs: i32[] = [10, 20, 30];
    let ys = xs;
    ys = ys.with(1, 999);
    if (xs[1] != 20) { return 1; }
    if (ys[1] != 999) { return 2; }
    return 0;
}`},
		{"map_set", `
import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    let n = m;
    n = n.insert("a", 999);
    if (m.get_or("a", -1) != 1) { return 1; }
    if (n.get_or("a", -1) != 999) { return 2; }
    return 0;
}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("main returned %d, want 0 (aliased mutation must copy)", got)
			}
		})
	}
}

// TestWASMRcDecReclaim is the dec-side companion to the CoW cases: a drop
// releases what it holds (so nothing is left live, and nothing is released
// twice), an overwrite frees the prior value, and heavy churn through the
// freelist stays correct.
func TestWASMRcDecReclaim(t *testing.T) {
	// Dropping an array that holds a shared element releases that element's
	// retain: the census balances. Were the dec skipped, `inner` would
	// outlive main with one count too many.
	t.Run("drop_dec_fires", func(t *testing.T) {
		_, stderr, code := runLeakCheckWasm(t, `function consume(inner: u8[]): i32 {
    let outer: u8[][] = [inner];
    return 0;
}
function main(): i32 {
    let inner: u8[] = __alloc_u8(4);
    let ignore: i32 = consume(inner);
    return 0;
}`, false)
		if code != 0 {
			t.Fatalf("exit=%d, want 0", code)
		}
		allocs, frees, live := parseWasmLeakCheckLine(t, stderr)
		if allocs == 0 || allocs != frees || live != 0 {
			t.Errorf("got allocs=%d frees=%d live_bytes=%d, want balanced (the drop must release its element)", allocs, frees, live)
		}
	})
	cases := []struct {
		name string
		src  string
	}{
		// Nesting fresh + aliased elements and dropping the outer
		// array yields correct values and no over-release.
		{"drop_no_over_release", `function build(): i32 {
    let inner: i32[] = [1, 2, 3];
    let a: i32[][] = [inner];        // aliased element (inc'd)
    let b: i32[][] = [[4, 5], [6]];  // fresh elements (not inc'd)
    return a[0][1] + b[1][0];        // 2 + 6
}
function main(): i32 {
    return (build() - 8) + __rc_underflow_count();
}`},
		// Overwrite-set frees the prior value; no over-release.
		{"dec_on_overwrite", `
import "core/map";
function main(): i32 {
    let m: Map[i32, i32[]] = map_new(4);
    let i: i32 = 0;
    while (i < 64) { m = m.insert(7, [i, i + 1, i + 2]); i = i + 1; }
    let v: i32[] = m.get_or(7, []);
    return (v[2] - 65) + __rc_underflow_count();
}`},
		// Freelist reuse: heavy alloc churn stays correct and bounded
		// (a corrupted freelist would trap or mis-read here).
		{"churn_freelist_reuse", `function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let xs: i32[] = [i, i + 1, i + 2, i + 3];
        acc = acc + xs[3] - xs[0];   // always 3
        i = i + 1;
    }
    return (acc - 1500) + __rc_underflow_count();  // 500 * 3
}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("main returned %d, want 0 (dec must fire)", got)
			}
		})
	}
}
