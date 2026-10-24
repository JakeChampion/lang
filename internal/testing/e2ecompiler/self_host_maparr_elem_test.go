package e2ecompiler

import "testing"

var mapArrElemCases = []struct {
	name string
	src  string
	want int
}{
	{"maparr-foreach", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let ms: Map[string, i32][] = [m, m];
    let acc: i32 = 0;
    for x in ms { acc = acc + x.get_or("k", 0); }
    return acc + ms.len();
}`, 16},
	{"maparr-tuple-elem-index", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let ms: Map[string, i32][] = [m];
    let t: (Map[string, i32][], i32) = (ms, 3);
    return t.0[0].get_or("k", 0) + t.1;
}`, 10},
	{"maparr-struct-field-index-and-len", `import "core/map";
struct Reg { rows: Map[string, i32][] }
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let r: Reg = Reg { rows: [m] };
    return r.rows[0].get_or("k", 0) + r.rows.len();
}`, 8},
	{"maparr-struct-field-len-only", `import "core/map";
struct Reg { rows: Map[string, i32][] }
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let r: Reg = Reg { rows: [m, m, m] };
    return r.rows.len();
}`, 3},
	{"plain-map-struct-field-unchanged", `import "core/map";
struct Cfg { caps: Map[string, i32] }
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("a", 3);
    m = m.insert("b", 4);
    let c: Cfg = Cfg { caps: m };
    return c.caps.len() + c.caps.get_or("a", 0) + c.caps.get_or("b", 0);
}`, 9},
	{"plain-map-tuple-elem-unchanged", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("a", 5);
    let t: (Map[string, i32], i32) = (m, 2);
    return t.0.len() + t.0.get_or("a", 0) + t.1;
}`, 8},
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
