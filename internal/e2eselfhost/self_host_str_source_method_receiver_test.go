package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// strSourceMethodReceiverCases pin the release of a fresh anonymous RECEIVER at a
// SOURCE-DECLARED string method, and the three callee shapes that must not get it.
//
// The builtin twin lands in lower_str_method; this is the primitive-method
// dispatch, which already stashed its ARGUMENTS through stash_fresh_str_arg and
// never its receiver — so `w(pre).copies()` stranded 46 B/round.
//
// The warrant is the callee-side proof recv_borrow_fns_of already computes, which
// was gated to STRUCT receivers. A string receiver earns the plain
// "<Type>.<method>" key on body_unsafe_for alone: the two field-hazard predicates
// beside it are about carrying a FIELD of the receiver out, and a string has none.
// body_unsafe_for refuses a bare `return s` and a `return s[a:b]` view alike,
// because both are escapes for it, and it refuses a receiver moved into a struct
// the callee hands back.
//
// The flat cases return 98 when the receiver is stranded. The three refusals all
// exit 97 under a compiler that releases the receiver without consulting the
// proof — including the VIEW case, which is witnessed here where the same
// question was contract-only at two earlier sites.
var strSourceMethodReceiverCases = []struct {
	name string
	src  string
	want int
}{
	// The shape that led here: a fresh receiver at a SOURCE-DECLARED method. The
	// builtin twin was released at lower_str_method; this is the primitive-method
	// dispatch, which stashed its ARGUMENTS already and never its receiver.
	// 46 B/round before, flat after.
	{"str-fresh-receiver-source-method-copy-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function (s: string) copies(): string { return s + ""; }
function round(pre: string): i32 { var u: string = w(pre).copies(); return u.len(); }
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
	// The method's body is irrelevant to the SITE: a result that never mentions the
	// receiver leaked 47 exactly as the copying one leaked 46. What the body decides
	// is only whether the callee-side proof admits it.
	{"str-fresh-receiver-source-method-unrelated-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function (s: string) unrel(): string { if (s.len() > 0) { return "xxxxxxxxxxxxxxxxxxxx"; } return "yyyyyyyyyyyyyyyyyyyy"; }
function round(pre: string): i32 { var u: string = w(pre).unrel(); return u.len(); }
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
	// Two links, both source-declared: the inner result is the outer's fresh receiver,
	// so one admission covers both.
	{"str-fresh-receiver-source-method-chained-flat", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function (s: string) copies(): string { return s + ""; }
function round(pre: string): i32 { return w(pre).copies().copies().len(); }
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
	// NEGATIVE: `return s` hands the receiver's box back, so releasing the receiver
	// frees what the caller now holds. body_unsafe_for refuses it — a bare ident is
	// an escape — and the plain recv_borrow key is therefore absent. Exits 97 when
	// admitted anyway.
	{"str-identity-return-method-receiver-refused", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function (s: string) ident(): string { return s; }
function round(pre: string): i32 {
    var t: str = w(pre).ident();
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (t.index_of("XXXX") >= 0) { return 0 - 2; }
    if (!t.starts_with("abcdefgh-a-wide")) { return 0 - 1; }
    return t.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != 106) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// NEGATIVE: `return s[2:s.len()]` is a VIEW over the receiver's buffer. Worth
	// noting that this distinction has a WITNESS here — it exits 97 when admitted —
	// where the same view question was contract-only at the binding-credit and
	// fresh-alloc-builtin sites. body_unsafe_for refuses it because a slice outside
	// a borrow position is an escape.
	{"str-view-return-method-receiver-refused", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function (s: string) view(): string { return s[2:s.len()]; }
function round(pre: string): i32 {
    var t: str = w(pre).view();
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (t.index_of("XXXX") >= 0) { return 0 - 2; }
    if (!t.starts_with("cdefgh-a-wide")) { return 0 - 1; }
    return t.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != 104) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// NEGATIVE: the receiver is moved into a struct field inside the callee and read
	// back out. Nothing about the RESULT gives this away — it is a fresh-looking
	// string — so the escape check is what catches it. Also exits 97 when admitted.
	{"str-receiver-stored-in-struct-refused", `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
struct Hold { v: string }
function stash(s: string): Hold { return Hold { v: s }; }
function (s: string) keeps(): string { var h: Hold = stash(s); return h.v; }
function round(pre: string): i32 {
    var t: str = w(pre).keeps();
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (t.index_of("XXXX") >= 0) { return 0 - 2; }
    if (!t.starts_with("abcdefgh-a-wide")) { return 0 - 1; }
    return t.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != 106) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
}

// TestSelfHostStrSourceMethodReceiverIRX86_64 drives the cases through the
// self-hosted x86-64 compiler.
func TestSelfHostStrSourceMethodReceiverIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strSourceMethodReceiverCases {
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
				t.Errorf("%s = %d, want %d (98 = the receiver was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrSourceMethodReceiverIRArm64 is the arm64 leg; the admission is shared
// irlower and the release is a per-backend transcription.
func TestSelfHostStrSourceMethodReceiverIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strSourceMethodReceiverCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the receiver was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrSourceMethodReceiverWasmIR is the wasm leg, where the release maps
// to $__fern_arr_dec on the rc-headered block.
func TestSelfHostStrSourceMethodReceiverWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping source-method receiver wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strSourceMethodReceiverCases {
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
				t.Errorf("%s = %d, want %d (98 = the receiver was stranded; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
