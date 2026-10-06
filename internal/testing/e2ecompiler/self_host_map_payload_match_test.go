package e2ecompiler

import "testing"

// mapPayloadMatchCases pin `match` on an Option/Result whose payload is a Map,
// bound from either variant, used or unused, and with an array value type. Each
// case runs twice and compares, so a payload read through the wrong box shows
// up as 97 rather than a lucky exit code.
var mapPayloadMatchCases = []struct {
	name string
	src  string
	want int
}{
	// A Map payload bound from Some and read.
	{"opt-map-payload-match", `import "core/map";
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[string, i32] = map_new(4);
        m = m.insert("k", i);
        let o: Option[Map[string, i32]] = Some(m);
        let r: i32 = 0;
        match (o) { Some(v) => { r = v.get_or("k", 0) + v.len(); }, None => {} }
        acc = (acc + r) % 83;
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
	// The payload BOUND BUT UNUSED.
	{"opt-map-payload-unused", `import "core/map";
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[string, i32] = map_new(4);
        m = m.insert("k", i);
        let o: Option[Map[string, i32]] = Some(m);
        let r: i32 = 0;
        match (o) { Some(v) => { r = 1; }, None => {} }
        acc = (acc + r + m.get_or("k", 0)) % 83;
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
	// The Ok side of a Result.
	{"result-map-ok-payload", `import "core/map";
function pick(n: i32): Result[Map[string, i32], string] {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", n);
    if (n < 0) { return Err("neg"); }
    return Ok(m);
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let r: i32 = 0;
        match (pick(i)) { Ok(v) => { r = v.get_or("k", 0); }, Err(e) => { r = e.len(); } }
        acc = (acc + r) % 83;
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
}`, 6},
	// The Err side: the commas inside `Map[string, i32]` must not confuse the
	// T/E split.
	{"result-map-err-payload", `import "core/map";
function pick(n: i32): Result[i32, Map[string, i32]] {
    let m: Map[string, i32] = map_new(4);
    m = m.insert("k", n);
    if (n % 2 == 0) { return Err(m); }
    return Ok(n);
}
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let r: i32 = 0;
        match (pick(i)) { Ok(v) => { r = v; }, Err(e) => { r = e.get_or("k", 0) + e.len(); } }
        acc = (acc + r) % 83;
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
}`, 8},
	// A Map whose VALUE type is an array — the payload spelling carries nested
	// brackets, and the bound slot must still dispatch `.get(k)` as a map op.
	{"opt-map-of-array-payload", `import "core/map";
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[string, i32[]] = map_new(4);
        m = m.insert("k", [i, i + 2]);
        let o: Option[Map[string, i32[]]] = Some(m);
        let r: i32 = 0;
        match (o) {
            Some(v) => { match (v.get("k")) { Some(xs) => { r = xs[0] + xs[1]; }, None => {} } },
            None => {}
        }
        acc = (acc + r) % 83;
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
}`, 20},
	// `Map[K, V][]` is an ARRAY OF MAPS: `v.len()` is the array's length, not a
	// map's.
	{"opt-map-array-payload", `import "core/map";
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let m: Map[string, i32] = map_new(4);
        m = m.insert("k", i);
        let ms: Map[string, i32][] = [m];
        let o: Option[Map[string, i32][]] = Some(ms);
        let r: i32 = 0;
        match (o) { Some(v) => { r = v.len(); }, None => {} }
        acc = (acc + r) % 83;
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
}`, 4},
}

const mapPayloadMatchFailFmt = "%s = %d, want %d (99 = over-release; 97 = value corrupted)"

func TestSelfHostMapPayloadMatchIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapPayloadMatchCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.want {
				t.Errorf(mapPayloadMatchFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostMapPayloadMatchIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapPayloadMatchCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf(mapPayloadMatchFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostMapPayloadMatchWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapPayloadMatchCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != tc.want {
				t.Errorf(mapPayloadMatchFailFmt, tc.name, code, tc.want)
			}
		})
	}
}
