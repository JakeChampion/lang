package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const strArrJoinPrelude = strProbeHelpers + `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
`

func strArrJoinHeap(n, limit int) string {
	elems := make([]string, n)
	for i := range elems {
		elems[i] = fmt.Sprintf(`w("e%d")`, i)
	}
	return strArrJoinPrelude + `function round(pre: string): i32 {
    let xs: string[] = [` + strings.Join(elems, ", ") + `];
    let s: string = xs.join("|");
    return s.len() % 251;
}
function churn(pre: string, n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + round(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let pre: string = "abcdefgh";
    let a: i32 = churn(pre, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(pre, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
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
	{"strarr-join-elements-live", strArrJoinPrelude + `function round(pre: string): i32 {
    let xs: string[] = [w("e0"), w("e1"), w("e2")];
    let s: string = xs.join("|");
    let n: i32 = s.len();
    let p1: string = w("XXXXXXXX");
    let p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (xs.len() != 3) { return 0 - 1; }
    if (!has_prefix(xs[0], "e0-")) { return 0 - 2; }
    if (!has_prefix(xs[2], "e2-")) { return 0 - 3; }
    if (has_sub(xs[1], "XXXX")) { return 0 - 4; }
    if (!has_prefix(s, "e0-")) { return 0 - 5; }
    if (!has_sub(s, "|")) { return 0 - 6; }
    if (n != 302) { return 0 - 7; }
    let again: string = xs.join("-");
    if (again.len() != n) { return 0 - 8; }
    return 3;
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { if (round(pre) != 3) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"strarr-join-array-escapes", strArrJoinPrelude + `function build(pre: string): string[] {
    let xs: string[] = [w("e0"), w("e1")];
    let s: string = xs.join("|");
    if (s.len() < 0) { return []; }
    return xs;
}
function round(pre: string): i32 {
    let ys: string[] = build(pre);
    let p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (ys.len() != 2) { return 0 - 1; }
    if (!has_prefix(ys[0], "e0-")) { return 0 - 2; }
    return ys.len();
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { if (round(pre) != 2) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"strarr-join-param-receiver-live", strArrJoinPrelude + `function joined(xs: string[]): i32 {
    let s: string = xs.join("|");
    return s.len() % 251;
}
function round(xs: string[]): i32 {
    let n: i32 = joined(xs);
    let p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (!has_prefix(xs[0], "e0-")) { return 0 - 1; }
    if (has_sub(xs[1], "XXXX")) { return 0 - 2; }
    return n;
}
function main(): i32 {
    let xs: string[] = [w("e0"), w("e1")];
    let i: i32 = 0;
    let want: i32 = round(xs);
    while (i < 2000) { if (round(xs) != want) { return 97; } i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`},
	{"strarr-join-param-receiver", strArrJoinPrelude + `function joined(xs: string[]): i32 {
    let s: string = xs.join("|");
    return s.len() % 251;
}
function churn(xs: string[], n: i32): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + joined(xs)) % 251; i = i + 1; } return acc; }
function main(): i32 {
    let xs: string[] = [w("e0"), w("e1"), w("e2")];
    let a: i32 = churn(xs, 400);
    let b1: i32 = (__heap_bump_bytes() as i32);
    let b: i32 = churn(xs, 400);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (a != b) { return 97; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`},
	{"strarr-join-param-user-method-not-credited", strArrJoinPrelude + `struct Holder { name: string, tag: string }
function (h: Holder) join(sep: string): string { return h.name; }
function joined(h: Holder): i32 { let s: string = h.join("|"); return s.len() % 251; }
function churn(pre: string): i32 { let a: string = w(pre + "1"); let b: string = w(pre + "2"); return a.len() + b.len(); }
function main(): i32 {
    let keep: Holder = Holder { name: w("aaaa"), tag: w("bbbb") };
    let i: i32 = 0;
    while (i < 2000) {
        if (joined(keep) < 0) { return 96; }
        if (churn("QQQQQQQQ") < 0) { return 95; }
        if (!has_prefix(keep.name, "aaaa-")) { return 97; }
        if (!has_prefix(keep.tag, "bbbb-")) { return 97; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`},
	{"strarr-join-user-method-not-credited", strArrJoinPrelude + `struct Holder { name: string, tag: string }
function (h: Holder) join(sep: string): string { return h.name; }
function churn(pre: string): i32 { let a: string = w(pre + "1"); let b: string = w(pre + "2"); let c: string = w(pre + "3"); return a.len() + b.len() + c.len(); }
function round(h: Holder): i32 {
    let s: string = h.join("|");
    return s.len() % 251;
}
function main(): i32 {
    let keep: Holder = Holder { name: w("aaaa"), tag: w("bbbb") };
    let i: i32 = 0;
    while (i < 2000) {
        if (round(keep) < 0) { return 96; }
        if (churn("QQQQQQQQ") < 0) { return 95; }
        if (!has_prefix(keep.name, "aaaa-")) { return 97; }
        if (!has_prefix(keep.tag, "bbbb-")) { return 97; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
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

func TestSelfHostStrArrJoinBorrowIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

func TestSelfHostStrArrJoinBorrowIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

func TestSelfHostStrArrJoinBorrowWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping strarr join borrow wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range strArrJoinSources(true) {
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
			if got := rcmd.ProcessState.ExitCode(); got != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, got, strArrJoinExitHint)
			}
		})
	}
}
