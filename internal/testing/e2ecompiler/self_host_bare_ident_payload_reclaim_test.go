package e2ecompiler

import "testing"

var bareIdentPayloadReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"bare-ident-array-payload", `function churn(n: i32): i32 {
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
	{"bare-ident-array-payload-dead", `function churn(n: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let xs: i32[] = [i, i + 2];
        let t: (i32, Option[i32[]]) = (i, Some(xs));
        let r: i32 = t.0;
        match (t.1) { Some(v) => { r = r + v[0]; }, None => {} }
        acc = (acc + r) % 91;
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
	{"bare-ident-array-carried-out", `function churn(n: i32): i32 {
    let keep: i32[] = [0, 0];
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let xs: i32[] = [i, i + 2];
        let t: (i32, Option[i32[]]) = (i, Some(xs));
        match (t.1) { Some(v) => { keep = v; }, None => {} }
        acc = (acc + t.0) % 91;
        i = i + 1;
    }
    let d1: i32[] = [777, 888];
    let d2: i32[] = [999, 555];
    return (acc + keep[0] + keep[1] + d1[0] + d2[0]) % 97;
}
function main(): i32 {
    let w: i32 = churn(100);
    let x: i32 = churn(100);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 72},
	{"bare-ident-string-refused", `import "std/i32";
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
    let x: i32 = churn(1000);
    if (__rc_underflow_count() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 91;
}`, 46},
}

const bareIdentPayloadFailFmt = "%s = %d, want %d (a small non-zero on a byte case is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostBareIdentPayloadReclaimIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range bareIdentPayloadReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.want {
				t.Errorf(bareIdentPayloadFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostBareIdentPayloadReclaimIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range bareIdentPayloadReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf(bareIdentPayloadFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostBareIdentPayloadReclaimWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range bareIdentPayloadReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); got != tc.want {
				t.Errorf(bareIdentPayloadFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
