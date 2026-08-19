package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// strChainReceiverCases pin the release of a FRESH-OR-RECEIVER CHAIN standing in
// receiver position at a source-declared string method — `base.tail(4).to_owned()`,
// where the intermediate is a view box over `base`'s bytes that nobody names.
//
// #7160 released a receiver is_fresh_str_temp could prove fresh. This chain is the
// shape it must REFUSE and must keep refusing: `tail` has an identity path
// (`if (n <= 0) { return s; }`), so on that path the "intermediate" IS the root's
// own box and freeing it is a double free. A static predicate cannot tell the two
// paths apart, because which one ran is a runtime fact.
//
// What settles it is the runtime discriminator the `.len()` site already uses: a
// box the chain freshly allocated is never the root's pointer. So the release is
// emitted under a pointer compare against sfrrecv_chain_root_slot's root, and the
// identity path simply does not take it. The outer callee still has to be
// recv_borrow proven — that is what says the CALL did not carry the receiver into
// its own result — which is why the `.ident()` case below is refused outright.
//
// Thresholds are calibrated, not inherited: the leak here is 24 B/round, so the
// 32768 the sibling suites use over 400 rounds would not catch it. Measured
// base/after on x86-64: 9600 -> 0 for each flat case, 0 -> 0 for the control.
const chainRecvPrelude = `import "std/i32";
import "std/i64";
import "std/string";
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function (s: string) tail(n: i32): str {
    if (n <= 0) { return s; }
    var sLen: i32 = s.len();
    if (n >= sLen) { return ""; }
    return s[n:sLen];
}
function (s: string) to_owned2(): string { return s + ""; }
function (s: string) unrel2(): string { if (s.len() > 0) { return "xxxxxxxxxxxxxxxxxxxx"; } return "yyyyyyyyyyyyyyyyyyyy"; }
function (s: string) ident2(): string { return s; }
`

// chainRecvHeap wraps a `round` body in the churn/heap-delta harness. 98 means the
// chain intermediate was stranded; 4096 sits 2.3x under the measured 9600 leak and
// far above the 0 a released chain produces.
func chainRecvHeap(round string) string {
	return chainRecvPrelude + `function round(pre: string): i32 { var base: string = w(pre); ` + round + ` }
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

var strChainReceiverCases = []struct {
	name string
	src  string
	want int
}{
	// The shape the divergence fixture is built on. `base.tail(4)` is a view box
	// over base's bytes; `.to_owned2()` is recv_borrow proven, so once the call
	// returns nothing names the view. 24 B/round before, flat after.
	{"str-chain-receiver-view-flat", chainRecvHeap(`return base.tail(4).to_owned2().len();`), 0},
	// The outer callee's RESULT is irrelevant to the site: `unrel2` never mentions
	// its receiver and the intermediate leaked exactly the same. recv_borrow is what
	// decides, not what comes back.
	{"str-chain-receiver-unrelated-result-flat", chainRecvHeap(`return base.tail(4).unrel2().len();`), 0},
	// Two proven links past the view root; the chain-root walk has to see through
	// both to reach `base`.
	{"str-chain-receiver-double-flat", chainRecvHeap(`return base.tail(4).to_owned2().to_owned2().len();`), 0},
	// CONTROL: no chain at all, already flat before this change. Pins that seeding
	// the release off the chain-root walk does not double-release the plain case.
	{"str-chain-receiver-plain-control", chainRecvHeap(`return base.to_owned2().len();`), 0},
	// WITNESS for the pointer compare. `tail(0)` takes the identity path, so the
	// "intermediate" is base's own box. A compiler that releases the chain receiver
	// unconditionally frees what `base` still holds, and the reads below exit 97.
	// Verified: 97 on a build with the compare removed, 0 with it.
	{"str-chain-receiver-identity-path-guarded", chainRecvPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var c: string = base.tail(0).to_owned2();
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    var p3: string = w("XXXXXXXX");
    if (p1.len() + p2.len() + p3.len() < 0) { return 0; }
    if (base.index_of("XXXX") >= 0) { return 0 - 1; }
    if (!base.starts_with("abcdefgh-a-wide")) { return 0 - 2; }
    if (!c.starts_with("abcdefgh-a-wide")) { return 0 - 3; }
    return base.len() + c.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { var r: i32 = round(pre); if (r != 212) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// The `n >= sLen` path returns a LITERAL "", whose box is in .rodata and whose
	// pointer differs from the root — so the compare admits it and the release runs
	// on a static. That is safe because __fern_str_view_free's view case guards on
	// the box base being inside the arena (base < heap_base or >= heap_end skips),
	// which is exactly what this case pins. Not a witness for the compare: it passes
	// with the compare removed too, because the guard doing the work is the other one.
	{"str-chain-receiver-empty-literal-path", chainRecvPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var c: string = base.tail(9999).to_owned2();
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (!base.starts_with("abcdefgh-a-wide")) { return 0 - 2; }
    if (c.len() != 0) { return 0 - 3; }
    return base.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 4000) { var r: i32 = round(pre); if (r != 106) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// REFUSED: the outer callee hands its receiver straight back, so it is not
	// recv_borrow proven and the chain release is never emitted. The result aliases
	// the view over base, which must survive being read after the call.
	{"str-chain-receiver-unproven-callee-refused", chainRecvPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var c: string = base.tail(4).ident2();
    var p1: string = w("ZZZZZZZZ");
    var p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (!base.starts_with("abcdefgh-a-wide")) { return 0 - 2; }
    if (!c.starts_with("efgh-a-wide")) { return 0 - 3; }
    return base.len() + c.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 4000) { var r: i32 = round(pre); if (r != 208) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
}

// TestSelfHostStrChainReceiverIRX86_64 drives the cases through the self-hosted
// x86-64 compiler.
func TestSelfHostStrChainReceiverIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strChainReceiverCases {
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
				t.Errorf("%s = %d, want %d (98 = the chain intermediate was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrChainReceiverIRArm64 is the arm64 leg; the admission and the
// pointer compare are shared irlower, the release a per-backend transcription.
func TestSelfHostStrChainReceiverIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strChainReceiverCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the chain intermediate was stranded; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrChainReceiverWasmIR is the wasm leg, where the release maps to
// $__fern_arr_dec on the rc-headered block.
func TestSelfHostStrChainReceiverWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping chain-receiver wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range strChainReceiverCases {
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
				t.Errorf("%s = %d, want %d (98 = the chain intermediate was stranded; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
