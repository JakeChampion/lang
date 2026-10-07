package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// strFreshAllocBuiltinCases pin that a fresh-ALLOCATING string builtin's result is
// itself a fresh temp, and that the builtins which do not allocate are not.
//
// A fresh temp is a box nobody else names, which a concat operand, map insert,
// call argument or `.len()` receiver may release once it is done with it. So
// `w(pre).to_ascii_upper().len()` must release the transform's own result as
// well as the receiver beneath it.
//
// The runtime bodies decide which builtins qualify — __fern_str_to_upper,
// _to_lower and _repeat all allocate unconditionally and return the new box,
// with repeat forcing a 1-byte cap so even an empty result allocates.
//
// The flat cases return 98 when the transform's result is stranded.
var strFreshAllocBuiltinCases = []struct {
	name string
	src  string
	want int
}{
	// `w(pre).to_ascii_upper()` is itself a fresh box: __fern_str_to_upper is
	// `__raw_alloc(n)` … `__raw_string(p, n)` with no identity path at all.
	// Stranded, it costs 46 B/round.
	{"str-fresh-builtin-len-receiver-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function round(pre: string): i32 { return w(pre).to_ascii_upper().len(); }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	// repeat forces a 1-byte cap when the total is zero, so even an empty result is a
	// fresh box. Its output is 2x the source, hence the wider ceiling.
	{"str-fresh-builtin-repeat-receiver-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function round(pre: string): i32 { return w(pre).repeat(2).len(); }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 65536) { return 98; }
    return 0;
}`, 0},
	// The same fresh builtin result as a concat operand must stay flat too.
	{"str-fresh-builtin-concat-operand-unchanged", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function round(pre: string): i32 { return (w(pre).to_ascii_upper() + "!").len(); }
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	// A fresh builtin result handed to a RETAINING callee: the struct field outlives
	// the call, so nothing may release it. Value-exact under same-size-class
	// pressure, so a wrongly freed buffer is recycled with different bytes.
	{"str-fresh-builtin-stored-still-live", strProbeHelpers + `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
struct Hold { v: string }
function stash(s: string): Hold { return Hold { v: s }; }
function round(pre: string): i32 {
    let h: Hold = stash(w(pre).to_ascii_upper());
    let p1: string = w("ZZZZZZZZ");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (has_sub(h.v, "XXXX")) { return 0 - 1; }
    if (!has_prefix(h.v, "ABCDEFGH")) { return 0 - 2; }
    return h.v.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 106) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
	// `.trim()`'s receiver is a live local, so releasing the trim result must
	// leave `b` intact: it is read back by value under same-size-class
	// pressure after the trim.
	{"str-trim-view-not-fresh-alloc", strProbeHelpers + `function w(pre: string): string { return "  " + pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789  "; }
function round(pre: string): i32 {
    let b: string = w(pre);
    let n: i32 = b.trim().len();
    let p1: string = w("ZZZZZZZZ");
    let p2: string = w("YYYYYYYY");
    let p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (has_sub(b, "XXXX")) { return 0 - 1; }
    if (!has_prefix(b, "  abcdefgh-a-wide")) { return 0 - 2; }
    return n;
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { let r: i32 = round(pre); if (r != 106) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`, 0},
}

// TestSelfHostStrFreshAllocBuiltinIRX86_64 drives the cases through the
// self-hosted x86-64 compiler.
func TestSelfHostStrFreshAllocBuiltinIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strFreshAllocBuiltinCases {
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
				t.Errorf("%s = %d, want %d (98 = the transform's result was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrFreshAllocBuiltinIRArm64 is the arm64 leg; the admission is shared
// lowering analysis and the release is a per-backend transcription.
func TestSelfHostStrFreshAllocBuiltinIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strFreshAllocBuiltinCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the transform's result was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrFreshAllocBuiltinWasmIR is the wasm leg, where the release maps
// to $__fern_arr_dec on the rc-headered block.
func TestSelfHostStrFreshAllocBuiltinWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping fresh-alloc builtin wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range strFreshAllocBuiltinCases {
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
				t.Errorf("%s = %d, want %d (98 = the transform's result was stranded; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
