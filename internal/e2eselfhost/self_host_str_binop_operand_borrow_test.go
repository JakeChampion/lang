package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// strBinopOperandBorrowCases pin that a BARE IDENT at a string binary operator's
// operand is a borrow, and that the positions which really do retain are not.
//
// expr_unsafe_for already carves out the index base (`name[i]`) and the method
// receiver (`name.m()`) as borrow reads. A concat operand was not carved out, so
// `return s + ""` — the most ordinary fresh-string body there is — made `s`
// escape, cost the parameter its borrowability, and left stash_fresh_str_arg
// refusing to release the temp the caller had just passed in. Concat copies both
// operands' bytes into a new box and the comparisons read them; neither retains.
//
// The flat cases return 98 when the argument temp is stranded: 400 rounds of a
// 170-byte producer is 68 KB against a 32 KB ceiling. The refusal cases are
// value-exact under same-size-class allocation pressure and verify the result
// only through borrow positions, so a wrongly released box is recycled with
// different bytes rather than merely freed and left readable.
var strBinopOperandBorrowCases = []struct {
	name string
	src  string
	want int
}{
	// The shape that led here. `cat`'s parameter is read by a concat and nothing
	// else, so the caller may release the temp it passed once the call returns —
	// but a bare ident anywhere was an escape, including at a concat operand, so
	// the parameter was not borrowable and stash_fresh_str_arg refused. 46 B/round
	// before, flat after.
	{"str-concat-operand-param-borrowable-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function cat(s: string): string { return s + ""; }
function round(pre: string): i32 { return cat(w(pre)).len(); }
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
	// The comparisons read their operands' bytes for the same reason concat copies
	// them, and lower_view_borrowed already treats both as borrow positions.
	{"str-compare-operand-param-borrowable-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function cmpsz(s: string): i32 { if (s > "m") { return 3; } return 5; }
function round(pre: string): i32 { return cmpsz(w(pre)); }
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
	// The control: a scalar-returning callee whose parameter never reaches a return
	// was already borrowable, so this was flat before and must stay flat.
	{"str-scalar-callee-temp-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function sz(s: string): i32 { return s.len(); }
function round(pre: string): i32 { return sz(w(pre)); }
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
	// NEGATIVE: `return s` hands the parameter's own box back, so releasing the
	// argument at the call site frees what the caller now holds. Exits 97 under a
	// compiler that makes every bare ident a borrow.
	{"str-identity-return-param-refused", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function keep(s: string): string { return s; }
function round(pre: string): i32 {
    var t: string = keep(w(pre));
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (!t.starts_with("abcdefgh-a-wide")) { return 0 - 1; }
    if (t.index_of("XXXX") >= 0) { return 0 - 2; }
    return t.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != 106) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// NEGATIVE: the parameter is moved into a struct field that outlives the call.
	// A struct-literal field value is not a borrow position and must not become one.
	// Also exits 97 when admitted.
	{"str-struct-stored-param-refused", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
struct Hold { v: string }
function stash(s: string): Hold { return Hold { v: s }; }
function via(s: string): string { var h: Hold = stash(s); return h.v; }
function round(pre: string): i32 {
    var t: string = via(w(pre));
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (!t.starts_with("abcdefgh-a-wide")) { return 0 - 1; }
    if (t.index_of("XXXX") >= 0) { return 0 - 2; }
    return t.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != 106) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// NEGATIVE: concat-read on one path, returned bare on the other. One escaping
	// path is enough to refuse the parameter — the call site has no way to tell
	// which path ran. Also exits 97 when admitted.
	{"str-mixed-path-param-refused", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function mixed(s: string, k: i32): string { if (k > 0) { return s + ""; } return s; }
function round(pre: string): i32 {
    var t: string = mixed(w(pre), 0);
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (!t.starts_with("abcdefgh-a-wide")) { return 0 - 1; }
    if (t.index_of("XXXX") >= 0) { return 0 - 2; }
    return t.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != 106) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
}

// TestSelfHostStrBinopOperandBorrowIRX86_64 drives the cases through the
// self-hosted x86-64 compiler.
func TestSelfHostStrBinopOperandBorrowIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strBinopOperandBorrowCases {
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
				t.Errorf("%s = %d, want %d (98 = the argument temp was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrBinopOperandBorrowIRArm64 is the arm64 leg; the borrowability
// verdict is shared irlower and the release is a per-backend transcription.
func TestSelfHostStrBinopOperandBorrowIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strBinopOperandBorrowCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the argument temp was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrBinopOperandBorrowWasmIR is the wasm leg, where the release maps
// to $__fern_arr_dec on the rc-headered block.
func TestSelfHostStrBinopOperandBorrowWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping binop-operand borrow wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strBinopOperandBorrowCases {
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
				t.Errorf("%s = %d, want %d (98 = the argument temp was stranded; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
