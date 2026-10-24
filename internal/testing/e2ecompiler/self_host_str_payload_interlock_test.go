package e2ecompiler

import "testing"

var strPayloadInterlockCases = []struct {
	name string
	src  string
	want int
}{
	{"str-payload-reclaimed", `import "std/i32";
function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let sv: string = "v" + i.to_string();
        let t: (i32, Option[string]) = (i, Some(sv));
        let r: i32 = t.0;
        match (t.1) { Some(v) => { r = r + v.len(); }, None => {} }
        acc = (acc + r + sv.len()) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(1000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	{"str-escapes-must-refuse", `import "std/i32";
function churn(n: i32): i32 {
    let keep: string = "";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let sv: string = "v" + i.to_string();
        let t: (i32, Option[string]) = (i, Some(sv));
        keep = sv;
        acc = (acc + t.0 + keep.len()) % 91;
        i = i + 1;
    }
    return (acc + keep.len()) % 91;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let x: i32 = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 91;
}`, 73},
	{"str-payload-carried-out", `import "std/i32";
function churn(n: i32): i32 {
    let keep: string = "zz";
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let sv: string = "v" + i.to_string();
        let t: (i32, Option[string]) = (i, Some(sv));
        match (t.1) { Some(v) => { keep = v; }, None => {} }
        acc = (acc + t.0) % 91;
        i = i + 1;
    }
    return (acc + keep.len()) % 91;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let x: i32 = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 91;
}`, 5},
	{"arr-payload-unchanged", `function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let xs: i32[] = [i, i + 2];
        let t: (i32, Option[i32[]]) = (i, Some(xs));
        let r: i32 = t.0;
        match (t.1) { Some(v) => { r = r + v[0]; }, None => {} }
        acc = (acc + r + xs[1]) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let w: i32 = churn(1000);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let x: i32 = churn(1000);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
}

const strPayloadInterlockFailFmt = "%s = %d, want %d (a small non-zero on a byte case is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostStrPayloadInterlockIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strPayloadInterlockCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.want {
				t.Errorf(strPayloadInterlockFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostStrPayloadInterlockIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range strPayloadInterlockCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf(strPayloadInterlockFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostStrPayloadInterlockWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range strPayloadInterlockCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != tc.want {
				t.Errorf(strPayloadInterlockFailFmt, tc.name, code, tc.want)
			}
		})
	}
}
