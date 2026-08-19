package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// unionPayloadReclaimCases pin the PAYLOAD of a tagged union at a tuple element.
//
// #7147 gave the union element its box dec, and left the payload alone on the
// grounds that releasing it needed the variant's own drop plan. The self-host
// already had that plan — emit_enum_variant_drops for a user enum, and
// emit_opt_payload_drop_via for a built-in Some/Ok/Err — so the element site
// only had to reach for it. A `(i, Some([i, i + 2]))` measured 40 B/round with
// the box freed and its array not; native is flat.
//
// What the payload release costs is decided by the SAME freshness rule the
// sibling element arms use one level up, so `(i, [a, b])` and `(i, Some([a, b]))`
// admit the same array. A bare-ident payload answers "" and keeps its leak: this
// site has no annotation, so it cannot tell a bare-ident array (which a flat dec
// releases) from a bare-ident string (which a flat dec would misread as a
// pointer on the two-word-string backends).
//
// Byte cases return measured bytes per round. The three that gate this change
// measure 40 | 40 | 24 on the parent, as x86-64 | arm64 | wasm; native is flat.
var unionPayloadReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"option-array-payload", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, Option[i32[]]) = (i, Some([i, i + 2]));
        var r: i32 = t.0;
        match (t.1) { Some(v) => { r = r + v[0]; }, None => {} }
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// A USER enum takes the runtime variant dispatch instead: read the tag,
	// release that variant's rc payload fields, then free the box.
	{"user-enum-array-payload", `enum Tag { Buf(i32[]), Nil }
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, Tag) = (i, Tag.Buf([i, i + 2]));
        var r: i32 = t.0;
        match (t.1) { Buf(v) => { r = r + v[0]; }, Nil => {} }
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// A built-in Result. #7160 took this shape from 80 to 40 by admitting Ok/Err
	// as constructions at all; this takes the remaining 40 — the payload — so the
	// two together close it. 40 on the parent, native flat.
	{"result-array-payload", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, Result[i32[], i32]) = (i, Ok([i, i + 2]));
        var r: i32 = t.0;
        match (t.1) { Ok(v) => { r = r + v[0]; }, Err(_) => {} }
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// A user enum the LOCAL-consume path refuses (string[] is neither scalar nor
	// rc-droppable, so enum_all_variants_rc_droppable says no) still gets its
	// constructed variant's payload released here: the emitter releases what it
	// can name and leaves the rest, which leaks rather than corrupts. This also
	// pins that a user variant spelled `Some` goes through variant dispatch and
	// NOT the built-in Option path, where the payload offset would fit it only by
	// coincidence. 40 on the parent.
	{"user-enum-nondroppable-sibling", `enum E { Some(i32[]), Other(string[]) }
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, E) = (i, E.Some([i, i + 2]));
        acc = (acc + t.0) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// IMMORTAL-LITERAL control rather than a leak gate: a `.rodata` literal is
	// not heap, so this shape was already flat. What it pins is the new
	// __fern_str_free on a payload that must not be freed — the rc-aware free
	// heap-guard-skips it, and an over-release would show as 99.
	{"string-literal-payload", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, Option[string]) = (i, Some("vv"));
        var r: i32 = t.0;
        match (t.1) { Some(v) => { r = r + v.len(); }, None => {} }
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// The arm CARRIES the payload out to a local read after the loop, so the
	// pointer outlives every reclaim point — and the payload is still released,
	// because the escaping binding retains it and the element's dec is spending
	// the tuple's own reference, not the last one.
	{"carried-out-payload-reclaimed", `function churn(n: i32): i32 {
    var keep: i32[] = [0, 0];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], Option[i32[]]) = (i, [i, i + 1], Some([i, i + 2]));
        match (t.2) { Some(v) => { keep = v; }, None => {} }
        acc = (acc + t.0) % 91;
        i = i + 1;
    }
    return (acc + keep[0] + keep[1]) % 91;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// USE-AFTER-FREE control, and the reason the case above is a reclaim rather
	// than a dangle. The carried-out pointer is read after decoy allocations
	// that would be handed the payload's block if it had really been freed;
	// `keep` still reads what it was given. 66 on native too.
	{"carried-out-payload-still-valid", `function churn(n: i32): i32 {
    var keep: i32[] = [0, 0];
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], Option[i32[]]) = (i, [i, i + 1], Some([i, i + 2]));
        match (t.2) { Some(v) => { keep = v; }, None => {} }
        i = i + 1;
    }
    var d1: i32[] = [777, 888];
    var d2: i32[] = [999, 555];
    var d3: i32[] = [321, 654];
    return (keep[0] + keep[1] + d1[0] + d2[0] + d3[0]) % 9973;
}
function main(): i32 {
    var w: i32 = churn(100);
    if (__rc_underflow() != 0) { return 99; }
    return w % 97;
}`, 66},
	// ALIASING control: the payload is a bare ident whose local is read after
	// the tuple is built. It keeps its leak rather than its correctness — the
	// value must be right and __rc_underflow() zero either way.
	{"bare-ident-payload-safe", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var xs: i32[] = [i, i + 2];
        var t: (i32, Option[i32[]]) = (i, Some(xs));
        var r: i32 = t.0;
        match (t.1) { Some(v) => { r = r + v[0]; }, None => {} }
        acc = (acc + r + xs[1]) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w % 91;
}`, 1},
}

const unionPayloadFailFmt = "%s = %d, want %d (a small non-zero on a byte case is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostUnionPayloadReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range unionPayloadReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(unionPayloadFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostUnionPayloadReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range unionPayloadReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(unionPayloadFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostUnionPayloadReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping union payload reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range unionPayloadReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf(unionPayloadFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
