package e2eselfhost

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
// is_fresh_str_temp answers "is this a box nobody else names" for concat operands,
// map inserts, call arguments and the `.len()` receiver. Its ExprCall arm admitted
// a scalar `.to_string()` and a proven fresh-ret free call, but not a string
// builtin — so `w(pre).to_ascii_upper().len()` stranded the transform's own result
// even after the receiver beneath it was released.
//
// str_fresh_alloc_method is the warrant, and it is strictly smaller than
// str_borrowing_method: that predicate is about what a method does to its
// RECEIVER and also admits the scalar predicates, while this one is about what it
// RETURNS. The runtime bodies decide it — __fern_str_to_upper, _to_lower, _reverse
// and _repeat all allocate unconditionally and return the new box, with repeat
// forcing a 1-byte cap so even an empty result allocates. `trim` returns a view and
// `replace` returns the receiver unchanged when the needle is absent.
//
// The flat cases return 98 when the transform's result is stranded; all three
// return 98 on a base-built compiler.
var strFreshAllocBuiltinCases = []struct {
	name string
	src  string
	want int
}{
	// The shape that led here. `w(pre).to_ascii_upper()` is itself a fresh box, but
	// is_fresh_str_temp did not know it — its ExprCall arm admitted a scalar
	// `.to_string()` and a proven fresh-ret free call, not a fresh-ALLOCATING string
	// builtin. The runtime body settles it: __fern_str_to_upper is `__raw_alloc(n)`
	// … `__raw_string(p, n)` with no identity path at all. 46 B/round before, flat
	// after.
	{"str-fresh-builtin-len-receiver-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function round(pre: string): i32 { return w(pre).to_ascii_upper().len(); }
function churn(pre: string, n: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var pre: string = "abcdefgh";
    var a: i32 = churn(pre, 400);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(pre, 400);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	// reverse allocates unconditionally too, and the same admission covers it.
	{"str-fresh-builtin-reverse-receiver-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function round(pre: string): i32 { return w(pre).reverse().len(); }
function churn(pre: string, n: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var pre: string = "abcdefgh";
    var a: i32 = churn(pre, 400);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(pre, 400);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	// repeat forces a 1-byte cap when the total is zero, so even an empty result is a
	// fresh box. Its output is 2x the source, hence the wider ceiling.
	{"str-fresh-builtin-repeat-receiver-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function round(pre: string): i32 { return w(pre).repeat(2).len(); }
function churn(pre: string, n: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var pre: string = "abcdefgh";
    var a: i32 = churn(pre, 400);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(pre, 400);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 65536) { return 98; }
    return 0;
}`, 0},
	// A CONTROL, not a teeth case: the concat operand path was already flat before
	// this change and must stay flat. The widening reaches emit_str_concat_reclaim,
	// map inserts and call arguments as well as the `.len()` receiver, so pinning no
	// regression there is worth the runtime.
	{"str-fresh-builtin-concat-operand-unchanged", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function round(pre: string): i32 { return (w(pre).to_ascii_upper() + "!").len(); }
function churn(pre: string, n: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var pre: string = "abcdefgh";
    var a: i32 = churn(pre, 400);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(pre, 400);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 32768) { return 98; }
    return 0;
}`, 0},
	// A fresh builtin result handed to a RETAINING callee: the struct field outlives
	// the call, so nothing may release it. Value-exact under same-size-class
	// pressure, so a wrongly freed buffer is recycled with different bytes.
	{"str-fresh-builtin-stored-still-live", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
struct Hold { v: string }
function stash(s: string): Hold { return Hold { v: s }; }
function round(pre: string): i32 {
    var h: Hold = stash(w(pre).to_ascii_upper());
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (h.v.index_of("XXXX") >= 0) { return 0 - 1; }
    if (!h.v.starts_with("ABCDEFGH")) { return 0 - 2; }
    return h.v.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != 106) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// NEGATIVE, and honestly a CONTRACT case rather than a witnessed one. `.trim()`
	// returns a zero-copy view, so it is outside str_fresh_alloc_method and its
	// source must stay live. Adding trim to that set does change the emission — one
	// extra __fern_str_free in `round` — but no probe I built turns that into an
	// observable fault, exactly as the same distinction was contract-only in the
	// binding credit. It stays excluded because the runtime body says view, not
	// because a test caught it.
	{"str-trim-view-not-fresh-alloc", `function w(pre: string): string { return "  " + pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789  "; }
function round(pre: string): i32 {
    var b: string = w(pre);
    var n: i32 = b.trim().len();
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (b.index_of("XXXX") >= 0) { return 0 - 1; }
    if (!b.starts_with("  abcdefgh-a-wide")) { return 0 - 2; }
    return n;
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != 106) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
}

// TestSelfHostStrFreshAllocBuiltinIRX86_64 drives the cases through the
// self-hosted x86-64 compiler.
func TestSelfHostStrFreshAllocBuiltinIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

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
// irlower and the release is a per-backend transcription.
func TestSelfHostStrFreshAllocBuiltinIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

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

	for _, tc := range strFreshAllocBuiltinCases {
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
				t.Errorf("%s = %d, want %d (98 = the transform's result was stranded; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
