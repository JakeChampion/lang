package e2ecompiler

import "testing"

var mapArrIdentCases = []struct {
	name string
	src  string
	want int
}{
	{"maparr-len", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let ms: Map[string, i32][] = [m];
    return ms.len();
}`, 1},
	{"maparr-len-and-index", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let ms: Map[string, i32][] = [m];
    return ms.len() + ms[0].get_or("k", 0);
}`, 8},
	{"maparr-index-only", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", 7);
    let ms: Map[string, i32][] = [m];
    return ms[0].get_or("k", 0);
}`, 7},
	{"plain-map-ops-unchanged", `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("a", 3);
    m = m.insert("b", 4);
    let got: i32 = 0;
    match (m.get("b")) { Some(v) => { got = v; }, None => {} }
    return m.len() + m.get_or("a", 0) + got;
}`, 9},
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
