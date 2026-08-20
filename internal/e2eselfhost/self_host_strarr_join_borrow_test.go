package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `xs.join(sep)` made its RECEIVER escape, so a string[] local that would
// otherwise be fully reclaimed leaked its buffer and every element.
//
// strarr_expr_unsafe treated any method call on the array by name as an escape
// with one exception hard-coded inline: `len`. That default is the right way
// round — a method returning an element, a slice, or the array itself hands out
// a lasting alias — but `join` belongs on the other side of it.
// __fern_arr_str_join walks the elements building a fresh accumulator with `+`
// and stores nothing, on every backend: the register one is Fern source
// (`asmcore.rt_src_arr_str_join`) written as `var r = ""` then concat precisely
// so it cannot alias, and wasm's `$__fern_str_join` copies bytes into a freshly
// boxed result.
//
// Measured, 400 rounds of the harness below, a pair of compilers from the same
// commit:
//
//	elements   x86-64                  arm64                   wasm
//	1          102400 -> 51200 -> 0    102400 -> 51200 -> 0    89600 -> 44800 -> 0
//	4          377600 -> 172800 -> 0   377600 -> 172800 -> 0   345600 -> 166400 -> 0
//	8          742400 -> 332800 -> 0   742400 -> 332800 -> 0   688000 -> 329600 -> 0
//
// Two arrows because it took two pieces: the receiver borrow above, then the
// RESULT credit below.
//
// The RESULT half needed a gate the receiver half did not. Crediting a
// `var s = xs.join(sep)` binding as fresh has to happen where the RECEIVER's
// type is known: str_local_binding_is_fresh is deliberately state-free, and a
// syntactic `field == "join"` arm there is UNSOUND — a user-declared
// `(h: Holder) join(sep)` returning `h.name` types as a string, so the result
// gets freed while the receiver still owns it. That version was written,
// measured at 0, and reverted; the fault is witnessed on both backends (exit 97
// on x86-64, a trap on wasm) and is pinned by the last case below.
//
// The credit therefore rides `join_strarr_init`, which reads the receiver's
// DECLARED type out of the body the way the `.to_string()` collector already
// does. Its limit is the same one: an unannotated receiver, or a `string[]`
// PARAM, carries no `var` declaration to read, so it is refused. A param
// receiver still leaks the result (131200 on x86-64), which is the next thing to
// pull on in this shape.
//
// The receiver half needed no such gate: the escape analysis runs over a slot
// already known to be `string[]`, and a user method cannot be called on one.

const strArrJoinPrelude = `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
`

// strArrJoinHeap builds a `round` that joins an n-element literal.
func strArrJoinHeap(n, limit int) string {
	elems := make([]string, n)
	for i := range elems {
		elems[i] = fmt.Sprintf(`w("e%d")`, i)
	}
	return strArrJoinPrelude + `function round(pre: string): i32 {
    var xs: string[] = [` + strings.Join(elems, ", ") + `];
    var s: string = xs.join("|");
    return s.len() % 251;
}
function churn(pre: string, n: i32): i32 { var acc: i32 = 0; var i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    var pre: string = "abcdefgh";
    var a: i32 = churn(pre, 400);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var b: i32 = churn(pre, 400);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= ` + fmt.Sprint(limit) + `) { return 98; }
    return 0;
}`
}

var strArrJoinHeapCases = []struct {
	name            string
	elems           int
	regMax, wasmMax int
}{
	{"strarr-join-1-element", 1, 4096, 4096},
	{"strarr-join-4-elements", 4, 4096, 4096},
	{"strarr-join-8-elements", 8, 4096, 4096},
}

var strArrJoinFaultCases = []struct {
	name string
	src  string
}{
	// The array and every element are read AFTER the join, behind decoys that
	// would be handed a freed block if the reclaim landed early. A second join
	// over the same array pins that the first one consumed nothing.
	{"strarr-join-elements-live", strArrJoinPrelude + `function round(pre: string): i32 {
    var xs: string[] = [w("e0"), w("e1"), w("e2")];
    var s: string = xs.join("|");
    var n: i32 = s.len();
    var p1: string = w("XXXXXXXX");
    var p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (xs.len() != 3) { return 0 - 1; }
    if (!xs[0].starts_with("e0-")) { return 0 - 2; }
    if (!xs[2].starts_with("e2-")) { return 0 - 3; }
    if (xs[1].index_of("XXXX") >= 0) { return 0 - 4; }
    if (!s.starts_with("e0-")) { return 0 - 5; }
    if (s.index_of("|") < 0) { return 0 - 6; }
    if (n != 302) { return 0 - 7; }
    var again: string = xs.join("-");
    if (again.len() != n) { return 0 - 8; }
    return 3;
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { if (round(pre) != 3) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`},
	// The array escapes by return, so the credit is withheld and the caller's
	// reads have to find the elements — a join in the same frame does not
	// change that verdict.
	{"strarr-join-array-escapes", strArrJoinPrelude + `function build(pre: string): string[] {
    var xs: string[] = [w("e0"), w("e1")];
    var s: string = xs.join("|");
    if (s.len() < 0) { return []; }
    return xs;
}
function round(pre: string): i32 {
    var ys: string[] = build(pre);
    var p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (ys.len() != 2) { return 0 - 1; }
    if (!ys[0].starts_with("e0-")) { return 0 - 2; }
    return ys.len();
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { if (round(pre) != 2) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`},
	// A `string[]` PARAM receiver: refused for want of a declaration to read the
	// type from, so the result still leaks — sound, and here so the limit is a
	// recorded fact rather than an assumption. Correctness only; no ceiling,
	// which would otherwise pin the leak as a floor.
	{"strarr-join-param-receiver-live", strArrJoinPrelude + `function joined(xs: string[]): i32 {
    var s: string = xs.join("|");
    return s.len() % 251;
}
function round(xs: string[]): i32 {
    var n: i32 = joined(xs);
    var p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (!xs[0].starts_with("e0-")) { return 0 - 1; }
    if (xs[1].index_of("XXXX") >= 0) { return 0 - 2; }
    return n;
}
function main(): i32 {
    var xs: string[] = [w("e0"), w("e1")];
    var i: i32 = 0;
    var want: i32 = round(xs);
    while (i < 2000) { if (round(xs) != want) { return 97; } i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`},
	// A USER method named `join` whose result aliases a field the receiver still
	// owns. Nothing may credit it, and this is what proves join_strarr_init's
	// receiver-type test carries its weight: a compiler with a bare syntactic
	// `field == "join"` in str_local_binding_is_fresh instead exits 97 here on
	// x86-64 and traps on wasm, while the heap cases above go to 0 either way.
	{"strarr-join-user-method-not-credited", strArrJoinPrelude + `struct Holder { name: string, tag: string }
function (h: Holder) join(sep: string): string { return h.name; }
function churn(pre: string): i32 { var a: string = w(pre + "1"); var b: string = w(pre + "2"); var c: string = w(pre + "3"); return a.len() + b.len() + c.len(); }
function round(h: Holder): i32 {
    var s: string = h.join("|");
    return s.len() % 251;
}
function main(): i32 {
    var keep: Holder = Holder { name: w("aaaa"), tag: w("bbbb") };
    var i: i32 = 0;
    while (i < 2000) {
        if (round(keep) < 0) { return 96; }
        if (churn("QQQQQQQQ") < 0) { return 95; }
        if (!keep.name.starts_with("aaaa-")) { return 97; }
        if (!keep.tag.starts_with("bbbb-")) { return 97; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`},
}

const strArrJoinExitHint = "98 = the joined array was stranded; 99 = over-release; 97 = value corrupted; 96/95 = the probe's own guards"

func strArrJoinSources(wasm bool) []struct{ name, src string } {
	var out []struct{ name, src string }
	for _, tc := range strArrJoinHeapCases {
		limit := tc.regMax
		if wasm {
			limit = tc.wasmMax
		}
		out = append(out, struct{ name, src string }{tc.name, strArrJoinHeap(tc.elems, limit)})
	}
	for _, tc := range strArrJoinFaultCases {
		out = append(out, struct{ name, src string }{tc.name, tc.src})
	}
	return out
}

// TestSelfHostStrArrJoinBorrowIRX86_64 is the x86-64 leg.
func TestSelfHostStrArrJoinBorrowIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strArrJoinSources(false) {
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
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, strArrJoinExitHint)
			}
		})
	}
}

// TestSelfHostStrArrJoinBorrowIRArm64 is the arm64 leg.
func TestSelfHostStrArrJoinBorrowIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strArrJoinSources(false) {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, strArrJoinExitHint)
			}
		})
	}
}

// TestSelfHostStrArrJoinBorrowWasmIR is the wasm leg.
func TestSelfHostStrArrJoinBorrowWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping strarr join borrow wasm IR e2e")
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

	for _, tc := range strArrJoinSources(true) {
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
			if got := rcmd.ProcessState.ExitCode(); got != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, got, strArrJoinExitHint)
			}
		})
	}
}
