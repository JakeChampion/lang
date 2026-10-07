package e2ecompiler

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
// The slice box is a view over the source's bytes, never the source's own box,
// so it is released once the call has returned. On wasm a slice is a COPY
// rather than the zero-copy view the register backends build (#4294), so there
// the leak scales with the payload; the wide-payload case pins that.
//
// A callee that returns its receiver hands the view straight back, and
// releasing the box then corrupts what the caller holds (exit 97).
const sliceViewPrelude = `import "std/i32";
import "std/i64";
import "std/string";
` + strProbeHelpers + `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function ww(pre: string): string { return pre + "-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment"; }
function (s: string) own2(): string { return s + ""; }
function (s: string) idv(): str { return s; }
`

// sliceViewHeap wraps a `round` body in the churn/heap-delta harness; the second
// churn must leave the heap bump flat.
func sliceViewHeap(producer string, round string) string {
	return sliceViewPrelude + `function round(pre: string): i32 { let base: string = ` + producer + `(pre); ` + round + ` }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 != b1) { return 98; }
    return 0;
}`
}

var strSliceViewReleaseCases = []struct {
	name string
	src  string
	want int
}{
	// The shape. 9600 before on the register backends, 48000 on wasm; flat after.
	{"str-slice-view-release-flat", sliceViewHeap("w", `return slice_unchecked(base, 4, base.len()).own2().len();`), 0},
	// The wasm half specifically: with a payload four times wider the register
	// residual would not move (a 24-byte box is a 24-byte box) but wasm's did,
	// 230400, because the slice copies. Both go to 0.
	{"str-slice-view-release-wide-payload", sliceViewHeap("ww", `return slice_unchecked(base, 4, base.len()).own2().len();`), 0},
	// A slice OF a slice: the root walk has to recurse through both links to reach
	// `base`, or it stops at the inner one and releases nothing. The inner slice is
	// the operand of the second slice, not a receiver, and must be released too.
	{"str-slice-view-release-nested-chain", sliceViewPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let c: string = slice_unchecked(slice_unchecked(base, 4, base.len()), 1, 20).own2();
    let p1: string = w("XXXXXXXX");
    let p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (has_sub(c, "XXXX")) { return 0 - 1; }
    if (!has_prefix(c, "fgh-a-wide")) { return 0 - 2; }
    if (!has_prefix(base, "abcdefgh-a-wide")) { return 0 - 3; }
    return base.len() + c.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 3000) { let r: i32 = round(pre); if (r != 125) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	// `idv` returns its receiver, so the call's result IS the view box; releasing
	// it frees what `v` still points at (exit 97).
	{"str-slice-view-release-identity-callee-refused", sliceViewPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let v: str = slice_unchecked(base, 4, base.len()).idv();
    let p1: string = w("XXXXXXXX");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("ZZZZZZZZ");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (has_sub(v, "XXXX")) { return 0 - 1; }
    if (!has_prefix(v, "efgh-a-wide")) { return 0 - 2; }
    if (!has_prefix(base, "abcdefgh-a-wide")) { return 0 - 3; }
    return v.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 3000) { let r: i32 = round(pre); if (r != 102) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	// A NAMED slice is not a receiver-position temp: `v` outlives the call and the
	// exit sweep owns it, so this arm must not fire. Both the view and the copy are
	// read afterwards.
	{"str-slice-view-release-named-slice-untouched", sliceViewPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let v: str = slice_unchecked(base, 4, base.len());
    let c: string = v.own2();
    let p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (!has_prefix(v, "efgh-a-wide")) { return 0 - 1; }
    if (!has_prefix(c, "efgh-a-wide")) { return 0 - 2; }
    return v.len() + c.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 3000) { let r: i32 = round(pre); if (r != 204) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	// The released box's SOURCE and the call's result both live on. Freeing the
	// 24-byte box must leave the shared bytes alone — that is the whole point of
	// __fern_str_view_free's immortal-rc case.
	{"str-slice-view-release-source-live", sliceViewPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let c: string = slice_unchecked(base, 4, base.len()).own2();
    let p1: string = w("XXXXXXXX");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("ZZZZZZZZ");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (has_sub(c, "XXXX")) { return 0 - 1; }
    if (!has_prefix(c, "efgh-a-wide")) { return 0 - 2; }
    if (!has_prefix(base, "abcdefgh-a-wide")) { return 0 - 3; }
    return base.len() + c.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 3000) { let r: i32 = round(pre); if (r != 208) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
}

// TestSelfHostStrSliceViewReleaseIRX86_64 drives the cases through the
// self-hosted x86-64 compiler, with the leak census on.
func TestSelfHostStrSliceViewReleaseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	t.Setenv("FERN_LEAKCHECK", "1")
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strSliceViewReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			stderr, code := hevRun(t, runner, bin)
			if code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the view box was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
			allocs, frees, live := parseLeakcheck(t, tc.name, stderr)
			if live != 0 || allocs != frees {
				t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want a balanced census", tc.name, allocs, frees, live)
			}
		})
	}
}

// TestSelfHostStrSliceViewReleaseIRArm64 is the arm64 leg.
func TestSelfHostStrSliceViewReleaseIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range strSliceViewReleaseCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
