package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// strFreshReceiverCases pin the release of a fresh ANONYMOUS RECEIVER at a string
// builtin method, and the two methods that must not get it.
//
// lower_str_method lowered its receiver and never released it, so
// `w(pre).to_ascii_upper()` stranded the temp at 46 B/round. `.len()` was always
// flat because it has its own release path, which made this look like a property
// of the method rather than a missing site. The fix reuses the ARGUMENT side's
// park/drain pair (stash_fresh_str_arg / free_stashed_str_args) — same shape, same
// net-zero load/free/drop under the live result.
//
// The warrant is str_borrowing_method: those methods read the receiver's bytes or
// allocate a new buffer and hand back neither the receiver's box nor a view of it.
// `trim` and `replace` are outside it because both can return the receiver, and
// `chars` / `lines` / `split` were never in it because their results carry views
// into the receiver's bytes. All of them still lower here and all of them keep the
// leak rather than risk the alias.
//
// The flat cases return 98 when the receiver is stranded: 400 rounds of a 170-byte
// producer is 68 KB against a 32 KB ceiling. The refusal cases are value-exact
// under same-size-class allocation pressure, so a wrongly released buffer is
// recycled with different bytes rather than merely freed and left readable.
var strFreshReceiverCases = []struct {
	name string
	src  string
	want int
}{
	// The shape that led here: a fresh receiver consumed by a copying builtin. The
	// op allocates a new buffer and the receiver is dead the instant it has been
	// read, but nothing released it — 46 B/round before, flat after. `.len()` was
	// always flat because it has its own release path, which is what made this look
	// like a property of the method rather than a missing site.
	{"str-fresh-receiver-copy-method-released-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function round(pre: string): i32 { var u: string = w(pre).to_ascii_upper(); return u.len(); }
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
	// A SCALAR-returning predicate on a fresh receiver: nothing survives the call at
	// all, so the receiver is doubly dead.
	{"str-fresh-receiver-predicate-released-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function round(pre: string): i32 { if (w(pre).contains("wide")) { return 3; } return 5; }
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
	// reverse allocates its own buffer, same as the case transforms.
	{"str-fresh-receiver-reverse-released-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function round(pre: string): i32 { var u: string = w(pre).reverse(); return u.len(); }
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
	// NEGATIVE: `.trim()` returns a zero-copy VIEW over the receiver's buffer, so
	// releasing the receiver leaves the result pointing at freed bytes. This is the
	// case that makes str_borrowing_method load-bearing here rather than merely
	// conservative: it exits 97 under a compiler that releases the receiver anyway.
	{"str-fresh-receiver-trim-view-refused", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function round(pre: string): i32 {
    var t: str = w(pre).trim();
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (t.index_of("XXXX") >= 0) { return 0 - 2; }
    if (!t.starts_with("abcdefgh-a-wide")) { return 0 - 1; }
    return t.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != 106) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// NEGATIVE: `.replace(a, b)` returns the receiver UNCHANGED when the needle is
	// absent, so the result can be the receiver's own box. Also exits 97 when the
	// receiver is released.
	{"str-fresh-receiver-replace-identity-refused", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function round(pre: string): i32 {
    var t: str = w(pre).replace("QQQQ", "R");
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (t.index_of("XXXX") >= 0) { return 0 - 2; }
    if (!t.starts_with("abcdefgh-a-wide")) { return 0 - 1; }
    return t.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != 106) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// A named local is not an anonymous temp, so it keeps its own scope-exit reclaim
	// and the call site must not release it. Control: is_fresh_str_temp refuses it
	// either way, and both the receiver and the copy stay readable afterwards.
	{"str-named-local-receiver-untouched", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function round(pre: string): i32 {
    var b: string = w(pre);
    var t: string = b.to_ascii_upper();
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (!b.starts_with("abcdefgh-a-wide")) { return 0 - 1; }
    if (!t.starts_with("ABCDEFGH-A-WIDE")) { return 0 - 2; }
    if (b.index_of("XXXX") >= 0 || t.index_of("XXXX") >= 0) { return 0 - 3; }
    return b.len() + t.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != 212) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
}

// TestSelfHostStrFreshReceiverIRX86_64 drives the cases through the
// self-hosted x86-64 compiler.
func TestSelfHostStrFreshReceiverIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strFreshReceiverCases {
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
				t.Errorf("%s = %d, want %d (98 = the receiver temp was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrFreshReceiverIRArm64 is the arm64 leg; the admission is shared
// irlower and the release is a per-backend transcription.
func TestSelfHostStrFreshReceiverIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strFreshReceiverCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the receiver temp was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrFreshReceiverWasmIR is the wasm leg, where the release maps
// to $__fern_arr_dec on the rc-headered block.
func TestSelfHostStrFreshReceiverWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping fresh-receiver release wasm IR e2e")
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

	for _, tc := range strFreshReceiverCases {
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
				t.Errorf("%s = %d, want %d (98 = the receiver temp was stranded; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
