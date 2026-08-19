package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// strSliceViewReleaseCases pin the release of an intermediate VIEW BOX standing
// in receiver position — `base[4:base.len()].to_owned()`, where the slice yields
// a box nothing names and the call is done with it once it returns.
//
// #7164 releases a fresh-or-receiver CHAIN in receiver position under a runtime
// pointer compare, but sfrrecv_chain_root_slot only walked an ExprCall link. A
// bare ExprSlice was not a link it followed, so the box survived: 9600 B over
// 400 rounds on the register backends and 48000 on wasm, where a slice is a COPY
// rather than the zero-copy view the asm-IR path builds (#4294). A slice link is
// the same character as an SFRRECV call — the box is a view over the source's
// bytes and never the source's own box — so walking through it to the root is
// all that was missing.
//
// Measured, two compilers built from the same commit, 400 rounds:
//
//	leg      before   after
//	x86-64     9600       0
//	arm64      9600       0
//	wasm      48000       0
//	wasm (wide payload)  230400 -> 0
//
// The wasm rows are the ones worth keeping: its residual scaled with the payload
// because the slice copies, so this recovers the whole copy rather than a
// 24-byte header.
//
// The two guards have DIFFERENT standing at this arm, which the cases record:
//
//   - recv_borrow is WITNESSED here. A callee that returns its receiver hands the
//     view straight back, and releasing the box corrupts what the caller holds:
//     exit 97 on a build with the gate dropped.
//   - the pointer compare is CONTRACT-ONLY here, and deliberately so. A slice box
//     is never the source's own box, so the compare cannot fire; every case below
//     passes on a build with it removed. It stays because the ExprCall arm it
//     shares does need it — #7164's identity-path probe still exits 97 without it.
const sliceViewPrelude = `import "std/i32";
import "std/i64";
import "std/string";
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function ww(pre: string): string { return pre + "-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment"; }
function (s: string) own2(): string { return s + ""; }
function (s: string) idv(): str { return s; }
`

// sliceViewHeap wraps a `round` body in the churn/heap-delta harness. 4096 is far
// under every measured leak here and far over the 0 a released box produces.
func sliceViewHeap(producer string, round string) string {
	return sliceViewPrelude + `function round(pre: string): i32 { var base: string = ` + producer + `(pre); ` + round + ` }
function churn(pre: string, n: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var pre: string = "abcdefgh";
    var a: i32 = churn(pre, 400);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(pre, 400);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`
}

var strSliceViewReleaseCases = []struct {
	name string
	src  string
	want int
}{
	// The shape. 9600 before on the register backends, 48000 on wasm; flat after.
	{"str-slice-view-release-flat", sliceViewHeap("w", `return base[4:base.len()].own2().len();`), 0},
	// The wasm half specifically: with a payload four times wider the register
	// residual would not move (a 24-byte box is a 24-byte box) but wasm's did,
	// 230400, because the slice copies. Both go to 0.
	{"str-slice-view-release-wide-payload", sliceViewHeap("ww", `return base[4:base.len()].own2().len();`), 0},
	// A slice OF a slice: the root walk has to recurse through both links to reach
	// `base`, or it stops at the inner one and releases nothing. This case pins the
	// VALUE rather than the bytes, because the two backends leave different things
	// behind and neither is what this change is about:
	//
	//	x86-64 / arm64   9600 -> 0     both boxes gone
	//	wasm            60800 -> 48000 the OUTER copy gone, the inner one stranded
	//
	// The wasm remainder is the INNER slice, which is not in receiver position at
	// all — it is the operand of the second slice — so no receiver-arm release can
	// reach it, and there it is a full payload copy rather than a 24-byte header.
	// That is the next lead on this shape, not something to gate here.
	{"str-slice-view-release-nested-chain", sliceViewPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var c: string = base[4:base.len()][1:20].own2();
    var p1: string = w("XXXXXXXX");
    var p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (c.index_of("XXXX") >= 0) { return 0 - 1; }
    if (!c.starts_with("fgh-a-wide")) { return 0 - 2; }
    if (!base.starts_with("abcdefgh-a-wide")) { return 0 - 3; }
    return base.len() + c.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 3000) { var r: i32 = round(pre); if (r != 125) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// WITNESS for the recv_borrow gate. `idv` returns its receiver, so the call's
	// result IS the view box; releasing it frees what `v` still points at. Exit 97
	// on a build with the gate dropped. body_unsafe_for refuses `idv` because a
	// bare `return s` is an escape, so the key is absent and nothing is emitted.
	{"str-slice-view-release-identity-callee-refused", sliceViewPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var v: str = base[4:base.len()].idv();
    var p1: string = w("XXXXXXXX");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("ZZZZZZZZ");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (v.index_of("XXXX") >= 0) { return 0 - 1; }
    if (!v.starts_with("efgh-a-wide")) { return 0 - 2; }
    if (!base.starts_with("abcdefgh-a-wide")) { return 0 - 3; }
    return v.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 3000) { var r: i32 = round(pre); if (r != 102) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// A NAMED slice is not a receiver-position temp: `v` outlives the call and the
	// exit sweep owns it, so this arm must not fire. Both the view and the copy are
	// read afterwards.
	{"str-slice-view-release-named-slice-untouched", sliceViewPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var v: str = base[4:base.len()];
    var c: string = v.own2();
    var p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (!v.starts_with("efgh-a-wide")) { return 0 - 1; }
    if (!c.starts_with("efgh-a-wide")) { return 0 - 2; }
    return v.len() + c.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 3000) { var r: i32 = round(pre); if (r != 204) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// The released box's SOURCE and the call's result both live on. Freeing the
	// 24-byte box must leave the shared bytes alone — that is the whole point of
	// __fern_str_view_free's immortal-rc case.
	{"str-slice-view-release-source-live", sliceViewPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var c: string = base[4:base.len()].own2();
    var p1: string = w("XXXXXXXX");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("ZZZZZZZZ");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (c.index_of("XXXX") >= 0) { return 0 - 1; }
    if (!c.starts_with("efgh-a-wide")) { return 0 - 2; }
    if (!base.starts_with("abcdefgh-a-wide")) { return 0 - 3; }
    return base.len() + c.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 3000) { var r: i32 = round(pre); if (r != 208) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
}

// TestSelfHostStrSliceViewReleaseIRX86_64 drives the cases through the
// self-hosted x86-64 compiler.
func TestSelfHostStrSliceViewReleaseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strSliceViewReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
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
				t.Errorf("%s = %d, want %d (98 = the view box was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrSliceViewReleaseIRArm64 is the arm64 leg.
func TestSelfHostStrSliceViewReleaseIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strSliceViewReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the view box was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrSliceViewReleaseWasmIR is the wasm leg, and the one that
// recovers a whole payload copy rather than a 24-byte header.
func TestSelfHostStrSliceViewReleaseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping slice view-release wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strSliceViewReleaseCases {
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
			watFile := filepath.Join(dir, strings.ReplaceAll(tc.name, "/", "_")+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s = %d, want %d (98 = the view box was stranded; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
