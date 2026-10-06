package e2ecompiler

import "testing"

// strPayloadInterlockCases pin a fresh STRING local handed to a union element of
// an rc-tuple — `let sv = …; let t = (i, Some(sv))`.
//
// #7168 released a bare-ident ARRAY payload there and refused a string. The
// refusal was right but the reason recorded for it was not: the release is not
// missing, it cannot reach zero alone. The construction retains the payload
// (rc 2) and the tuple SUPPRESSES the local's own sweep, so exactly one
// reference was ever spent. Forced at the call site, on x86-64:
//
//	neither half          2 __fern_str_free   32 B/round
//	the "STR:" credit     3                   32
//	the element release   3                   32
//	both                  4                    0
//
// So the two halves land together under one condition, which is what the ARRAY
// twin has always done: alloc + inc paid by the local's rebind/exit release AND
// the payload dec in the reclaim block.
//
// The credit half is the tuple-element twin of the #4354 closure interlock and
// sits beside it; the release half is gated on slot_is_reclaimable_str, so the
// two cannot drift apart.
var strPayloadInterlockCases = []struct {
	name string
	src  string
	want int
}{
	// The gate. 32 on the parent across all three backends; native flat. Reads
	// `sv` after the tuple, so it doubles as the aliasing control.
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
	// ESCAPE control: the same local also assigned to an outer one. The interlock
	// must NOT fire — tuple_union_payload_sole_use is not the thing that refuses
	// here, the plain escape walk is, because `keep = sv` is a second use outside
	// the tuple. It must stay refused AND must not over-release. 73 on native.
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
	// CARRIED-OUT control: the arm binds the payload and carries it past every
	// reclaim point. The binding must stay readable now that the payload is
	// actually released.
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
	// The ARRAY twin must be untouched by the interlock.
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

// TestSelfHostStrPayloadInterlockIRX86_64 runs the cases through the self-hosted CLI for x86-64.
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

// TestSelfHostStrPayloadInterlockIRArm64 is the arm64 leg, run under qemu.
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

// TestSelfHostStrPayloadInterlockWasmIR is the wasm32-wasi leg.
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
