package e2ecompiler

import "testing"

// mapArrElemCases pin the map ELEMENT of a `Map[K, V][]` reaching every
// receiver position, and the array itself reaching none of them.
//
// A `for` loop variable, `t.0[i].get(k)` and `r.rows[i].get(k)` must dispatch
// map ops on the element rather than fail with `unknown symbol i32.get_or`
// (#7195 was the ident half). A `Map[K, V][]` struct field or tuple element
// must still dispatch as an array: `r.rows.len()` lowered to op_map_len over
// array memory segfaults.
var mapArrElemCases = []struct {
	name string
	src  string
	want int
}{
	// Loop variable. Bailed before: the foreach binds the element directly and
	// marked struct / opt / tuple / arrarr element kinds but never a map.
	{"maparr-foreach", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let ms: Map[string, i32][] = [m, m];
    let acc: i32 = 0;
    for x in ms { acc = acc + x.get_or("k", 0); }
    return acc + ms.len();
}`, 16},
	// Tuple-element base. Bailed before.
	{"maparr-tuple-elem-index", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let ms: Map[string, i32][] = [m];
    let t: (Map[string, i32][], i32) = (ms, 3);
    return t.0[0].get_or("k", 0) + t.1;
}`, 10},
	// Struct-field base, reading the element AND the array's own length. Bailed
	// before on the element read; once that worked the `.len()` segfaulted.
	{"maparr-struct-field-index-and-len", `import "core/map";
struct Reg { rows: Map[string, i32][] }
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let r: Reg = Reg { rows: [m] };
    return r.rows[0].get_or("k", 0) + r.rows.len();
}`, 8},
	// The `.len()` alone, which is the segfault with nothing else in the way.
	{"maparr-struct-field-len-only", `import "core/map";
struct Reg { rows: Map[string, i32][] }
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let r: Reg = Reg { rows: [m, m, m] };
    return r.rows.len();
}`, 3},
	// NON-VACUITY on the array cases: a genuine Map STRUCT FIELD must still
	// dispatch every map op off the field.
	{"plain-map-struct-field-unchanged", `import "core/map";
struct Cfg { caps: Map[string, i32] }
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("a", 3);
    m = m.insert("b", 4);
    let c: Cfg = Cfg { caps: m };
    return c.caps.len() + c.caps.get_or("a", 0) + c.caps.get_or("b", 0);
}`, 9},
	// The same for a genuine Map TUPLE ELEMENT.
	{"plain-map-tuple-elem-unchanged", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("a", 5);
    let t: (Map[string, i32], i32) = (m, 2);
    return t.0.len() + t.0.get_or("a", 0) + t.1;
}`, 8},
	// Churn over the loop-var path, so a mis-typed dispatch cannot hide behind
	// a single-shot value check.
	{"maparr-foreach-churn", `import "core/map";
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[string, i32] = map_new(4);
        m = m.insert("k", i);
        let ms: Map[string, i32][] = [m, m];
        for x in ms { acc = (acc + x.get_or("k", 0)) % 83; }
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
}`, 12},
	// A DIRECT CALL receiver whose return type is `Map[K, V][]`: the un-bound
	// `mk().len()` receiver must type as an array, not a map (a segfault when
	// it did). Binding it to a local first (below) is the control.
	{"maparr-call-receiver-len", `import "core/map";
function mk(): Map[string, i32][] {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    return [m, m];
}
function main(): i32 {
    return mk().len();
}`, 2},
	{"maparr-call-bound-then-used", `import "core/map";
function mk(): Map[string, i32][] {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    return [m, m];
}
function main(): i32 {
    let a: Map[string, i32][] = mk();
    return a.len() + a[0].get_or("k", 0);
}`, 9},
	// A map-ARRAY PARAM. Already correct, and now pinned: the param column
	// records the ELEMENT map type like every other map-array site rather than
	// the array spelling it previously stored and happened to survive.
	{"maparr-param-len", `import "core/map";
function count(ms: Map[string, i32][]): i32 {
    return ms.len();
}
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    return count([m, m, m]);
}`, 3},
	{"maparr-param-index-and-len", `import "core/map";
function pick(ms: Map[string, i32][]): i32 {
    return ms[0].get_or("k", 0) + ms.len();
}
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    return pick([m, m]);
}`, 9},
	// A map-array reaching a tuple through a struct field, then read as an array.
	{"maparr-struct-field-into-tuple", `import "core/map";
struct Reg { rows: Map[string, i32][] }
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let r: Reg = Reg { rows: [m] };
    let t: (Map[string, i32][], i32) = (r.rows, 5);
    return t.0.len() + t.1;
}`, 6},
}

const mapArrElemFailFmt = "%s = %d, want %d (-1 = died on a signal: a map op dispatched on an array box; 99 = over-release; 97 = value corrupted)"

func TestSelfHostMapArrElemIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapArrElemCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.want {
				t.Errorf(mapArrElemFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostMapArrElemIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapArrElemCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf(mapArrElemFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostMapArrElemWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapArrElemCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != tc.want {
				t.Errorf(mapArrElemFailFmt, tc.name, code, tc.want)
			}
		})
	}
}
