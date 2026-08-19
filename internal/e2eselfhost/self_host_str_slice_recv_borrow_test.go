package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// strSliceRecvBorrowCases pin the escape scan's receiver carve-out reading the
// callee-side recv_borrow proof, not just the fixed builtin set.
//
// `base[4:base.len()].to_owned().len()` cost `base` its whole reclaim credit.
// expr_unsafe_for's ExprFieldAccess arm treats a non-ident receiver as a borrow
// only when str_borrowing_method names the field — a closed list of BUILTINS —
// so any source-declared method, `to_owned` included, sent the slice down the
// plain scan, where a slice is an alias and `base` escapes.
//
// The warrant already existed: recv_borrow carries the plain "<Type>.<method>"
// key exactly when body_unsafe_for found nothing carrying the receiver out. That
// admits `to_owned` (`return s + ""`) and refuses `trim` (`return s[low:high]`,
// a view of the receiver), which is the distinction that matters here. The
// registry is threaded through the scan family and is EMPTY inside
// recv_borrow_fns_of itself, the same Level-1 treatment `borrowable` documents,
// so computing the proof cannot consult it.
//
// The cases use fixture-local methods rather than std/string's, so the suite
// pins the mechanism rather than one stdlib body: `own2` is `to_owned`'s shape
// and `view2` is `trim`'s.
//
// Measured on x86-64, two compilers built from the same commit: 64000 -> 9600
// over 400 rounds, and the remaining 9600 — the intermediate VIEW BOX — is
// closed by the ExprSlice receiver release beside this, so the shape is now
// flat on all three legs.
const sliceRecvPrelude = `import "std/i32";
import "std/i64";
import "std/string";
function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
function ww(pre: string): string { return pre + "-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment-a-very-wide-payload-segment"; }
function (s: string) own2(): string { return s + ""; }
function (s: string) view2(): str { return s[1:s.len()]; }
`

// sliceRecvHeap wraps a `round` body in the churn/heap-delta harness. The gate
// sits between the two measured deltas rather than at flat: this change frees
// the SOURCE, and the view box it leaves behind is the next slice's business.
func sliceRecvHeap(round string) string {
	return sliceRecvPrelude + `function round(pre: string): i32 { var base: string = ww(pre); ` + round + ` }
function churn(pre: string, n: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var pre: string = "abcdefgh";
    var a: i32 = churn(pre, 400);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(pre, 400);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= @LIMIT@) { return 98; }
    return 0;
}`
}

// The gate was per-leg while a RESIDUAL remained — a fixed 24-byte view box on
// the register backends, a payload-sized copy on wasm, since a slice is
// zero-copy on the asm-IR path (#4294) and a copy on wasm. The sibling change
// that releases that box at an ExprSlice receiver takes every leg to 0, so one
// tight gate now serves all three and catches a regression of EITHER half: lose
// the source's credit and the leak is ~48 B/round, lose the box release and it
// is 24 (register) or payload-sized (wasm).
const sliceRecvLimit = "4096"

// sliceRecvSrc fills in the leg's heap gate. Cases without a gate carry no
// placeholder and pass through unchanged.
func sliceRecvSrc(src string, limit string) string {
	return strings.ReplaceAll(src, "@LIMIT@", limit)
}

var strSliceRecvBorrowCases = []struct {
	name string
	src  string
	want int
}{
	// The shape that led here. 64000 before, 9600 after; the gate at 32768 is
	// 2x under the leak and 3.4x over what remains.
	{"str-slice-recv-source-method-improved", sliceRecvHeap(`return base[4:base.len()].own2().len();`), 0},
	// CONTROL: no slice, already a borrow through the bare-ident receiver arm.
	// Flat (0) on both sides — pins that widening the carve-out does not disturb
	// the path that never needed it.
	{"str-slice-recv-plain-control", sliceRecvHeap(`return base.own2().len();`), 0},
	// WITNESS for the recv_borrow proof. `view2` returns a view of its receiver,
	// so the result aliases `base`'s buffer and is RETURNED past base's scope. A
	// compiler that admits any method name at this carve-out credits `base` as
	// merely borrowed, releases it at the return, and the caller reads freed and
	// reused bytes: exit 96, measured. body_unsafe_for refuses `view2` because a
	// slice outside a borrow position is an escape, so the key is absent.
	{"str-slice-recv-returned-view-witness", sliceRecvPrelude + `function leak(pre: string): str {
    var base: string = w(pre);
    return base[2:base.len()].view2();
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 3000) {
        var v: str = leak("abcdefgh");
        var p1: string = w("XXXXXXXX");
        var p2: string = w("YYYYYYYY");
        if (p1.len() + p2.len() < 0) { return 0; }
        if (v.index_of("XXXX") >= 0) { return 96; }
        if (v.len() != 103) { return 97; }
        if (!v.starts_with("defgh")) { return 95; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// The same view-returning method with the result NOT escaping the frame:
	// both the view and the source are read afterwards and must survive.
	{"str-slice-recv-view-method-live", sliceRecvPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var v: str = base[2:base.len()].view2();
    var p1: string = w("XXXXXXXX");
    var p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (v.index_of("XXXX") >= 0) { return 0 - 1; }
    if (!v.starts_with("defgh")) { return 0 - 2; }
    if (!base.starts_with("abcdefgh-a-wide")) { return 0 - 3; }
    return base.len() + v.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 3000) { var r: i32 = round(pre); if (r != 209) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`, 0},
	// The ADMITTED method's result and the source both live: `own2` copies, so
	// releasing nothing and reclaiming `base` at scope end must leave both intact.
	{"str-slice-recv-owned-live", sliceRecvPrelude + `function round(pre: string): i32 {
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
	// The key can only be spelled `string.<method>` — this scan has no types — so
	// a same-named method on another type answers to it. Here `Hold.own2` RETAINS
	// (it hands back a field holding `base`) while `string.own2` is proven
	// borrowing, and the result outlives the frame.
	//
	// CONTRACT-ONLY, and deliberately recorded as such: this passes with the
	// type-blind lookup unrestricted, because admitting a receiver only routes it
	// to expr_unsafe_for_view_pos, which differs from expr_unsafe_for on
	// ExprSlice alone. A non-slice receiver like `mk(base)` reaches the same
	// verdict either way, so no probe turns the blindness into a fault. A type
	// check here would be dead code rather than a guard.
	{"str-slice-recv-struct-name-collision", sliceRecvPrelude + `struct Hold { v: string }
function (h: Hold) own2(): string { return h.v; }
function mk(s: string): Hold { return Hold { v: s }; }
function leak2(pre: string): string {
    var base: string = w(pre);
    return mk(base).own2();
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 3000) {
        var c: string = leak2("abcdefgh");
        var p1: string = w("XXXXXXXX");
        var p2: string = w("YYYYYYYY");
        if (p1.len() + p2.len() < 0) { return 0; }
        if (c.index_of("XXXX") >= 0) { return 96; }
        if (c.len() != 106) { return 97; }
        if (!c.starts_with("abcdefgh-a-wide")) { return 95; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
}

// TestSelfHostStrSliceRecvBorrowIRX86_64 drives the cases through the
// self-hosted x86-64 compiler.
func TestSelfHostStrSliceRecvBorrowIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strSliceRecvBorrowCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(sliceRecvSrc(tc.src, sliceRecvLimit)+"\n"))
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
				t.Errorf("%s = %d, want %d (98 = the source lost its reclaim credit; 96 = a released source read back; 99 = over-release; 97/95 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrSliceRecvBorrowIRArm64 is the arm64 leg; the carve-out is
// shared irlower and the reclaim it unlocks is a per-backend transcription.
func TestSelfHostStrSliceRecvBorrowIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strSliceRecvBorrowCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(sliceRecvSrc(tc.src, sliceRecvLimit)+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = the source lost its reclaim credit; 96 = a released source read back; 99 = over-release; 97/95 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStrSliceRecvBorrowWasmIR is the wasm leg.
func TestSelfHostStrSliceRecvBorrowWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping slice-receiver borrow wasm IR e2e")
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

	for _, tc := range strSliceRecvBorrowCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(sliceRecvSrc(tc.src, sliceRecvLimit) + "\n"))
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
				t.Errorf("%s = %d, want %d (98 = the source lost its reclaim credit; 96 = a released source read back; 99 = over-release; 97/95 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
