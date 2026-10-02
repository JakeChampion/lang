package e2eselfhost

import "testing"

// mapArrIdentCases pin a `Map[K, V][]` IDENT not being typed as a single map.
//
// A map-array slot records its ELEMENT map type in the same column a plain map
// local uses, so that `ms[i].get(k)` can resolve K/V. Two readers took that
// column for the name itself without asking whether the slot is an array, so
// `ms` typed as one `Map[K, V]` and `ms.len()` lowered to op_map_len over array
// memory — a SEGFAULT, not a bail, where native answers 1.
//
// Both readers now go through LowerState.slot_map_type, which answers "" for an
// array slot. The array-ELEMENT readers are unaffected: they pair map_type_of
// with is_arr_slot explicitly, which is how `ms[i].get(k)` already worked and
// why it is a control here rather than a fix.
var mapArrIdentCases = []struct {
	name string
	src  string
	want int
}{
	// The gate. SEGFAULT before.
	{"maparr-len", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let ms: Map[string, i32][] = [m];
    return ms.len();
}`, 1},
	// The gate plus the element read, so the two paths are exercised on one
	// slot: `ms` as an array, `ms[0]` as a map. SEGFAULT before.
	{"maparr-len-and-index", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let ms: Map[string, i32][] = [m];
    return ms.len() + ms[0].get_or("k", 0);
}`, 8},
	// CONTROL: the element read alone already resolved through the ExprIndex
	// arm's own is_arr_slot check. It must keep working — a fix that answered
	// "" everywhere for the column would break this.
	{"maparr-index-only", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let ms: Map[string, i32][] = [m];
    return ms[0].get_or("k", 0);
}`, 7},
	// CONTROL: a genuine map local must still dispatch every map op off its
	// ident. This is the shape slot_map_type exists to keep answering.
	{"plain-map-ops-unchanged", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("a", 3);
    m = m.insert("b", 4);
    let got: i32 = 0;
    match (m.get("b")) { Some(v) => { got = v; }, None => {} }
    return m.len() + m.get_or("a", 0) + got;
}`, 9},
	// A churn loop over both paths: the reads must stay balanced, so a
	// mis-typed dispatch cannot hide behind a single-shot value check.
	{"maparr-churn-balanced", `import "core/map";
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[string, i32] = map_new(4);
        m = m.insert("k", i);
        let ms: Map[string, i32][] = [m];
        acc = (acc + ms.len() + ms[0].get_or("k", 0)) % 83;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let x: i32 = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 83;
}`, 10},
}

const mapArrIdentFailFmt = "%s = %d, want %d (-1 = died on a signal, which on the parent is the segfault from a map op dispatched on an array box; 99 = over-release; 97 = value corrupted)"

// TestSelfHostMapArrIdentIRX86_64 runs the cases through the self-hosted CLI for x86-64.
func TestSelfHostMapArrIdentIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapArrIdentCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.want {
				t.Errorf(mapArrIdentFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostMapArrIdentIRArm64 is the arm64 leg, run under qemu.
func TestSelfHostMapArrIdentIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapArrIdentCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf(mapArrIdentFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostMapArrIdentWasmIR is the wasm32-wasi leg.
func TestSelfHostMapArrIdentWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapArrIdentCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != tc.want {
				t.Errorf(mapArrIdentFailFmt, tc.name, code, tc.want)
			}
		})
	}
}
