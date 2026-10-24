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

const strTrimPrelude = strProbeHelpers + `function w(pre: string): string { return pre + "   -a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789   "; }
`

func strTrimHeap(body string, limit int) string {
	return strTrimPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
` + body + `
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

var strTrimHeapCases = []struct {
	name string
	body string
}{
	{"str-trim-view-released", `    let t: string = base.trim();
    return t.len() % 251;`},
	{"str-trim-of-trim-released", `    let t: string = base.trim();
    let u: string = t.trim();
    return (t.len() + u.len()) % 251;`},
}

var strTrimFaultCases = []struct {
	name string
	src  string
}{
	{"str-trim-source-and-result-live", strTrimPrelude + `function round(pre: string): i32 {
    let base: string = w(pre);
    let t: string = base.trim();
    let u: string = t.trim();
    let n: i32 = t.len();
    let p1: string = w("XXXXXXXX");
    let p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (!has_prefix(base, "abcdefgh   -")) { return 0 - 1; }
    if (!has_prefix(t, "abcdefgh   -")) { return 0 - 2; }
    if (!has_prefix(u, "abcdefgh   -")) { return 0 - 3; }
    if (has_sub(t, "XXXX")) { return 0 - 4; }
    if (has_sub(base, "YYYY")) { return 0 - 5; }
    if (u.len() != n) { return 0 - 6; }
    if (base.len() != n + 3) { return 0 - 7; }
    return 3;
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; while (i < 2000) { if (round(pre) != 3) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"str-trim-escapes-return", strTrimPrelude + `function trimmed(pre: string): string { let base: string = w(pre); let t: string = base.trim(); return t; }
function round(pre: string): i32 {
    let t: string = trimmed(pre);
    let p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (!has_prefix(t, "abcdefgh   -")) { return 0 - 1; }
    return t.len() % 251;
}
function main(): i32 { let pre: string = "abcdefgh"; let i: i32 = 0; let want: i32 = round(pre); while (i < 2000) { if (round(pre) != want) { return 97; } i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return 0; }`},
	{"str-trim-user-method-not-credited", strTrimPrelude + `struct Holder { name: string, tag: string }
function (h: Holder) trim(): string { return h.name; }
function trimmed(h: Holder): i32 { let t: string = h.trim(); return t.len() % 251; }
function churn(pre: string): i32 { let a: string = w(pre + "1"); let b: string = w(pre + "2"); return a.len() + b.len(); }
function main(): i32 {
    let keep: Holder = Holder { name: w("aaaa"), tag: w("bbbb") };
    let i: i32 = 0;
    while (i < 2000) {
        if (trimmed(keep) < 0) { return 96; }
        if (churn("QQQQQQQQ") < 0) { return 95; }
        if (!has_prefix(keep.name, "aaaa   -")) { return 97; }
        if (!has_prefix(keep.tag, "bbbb   -")) { return 97; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`},
}

const strTrimExitHint = "98 = the view box was stranded; 99 = over-release; 97 = value corrupted; 96/95 = the probe's own guards"

func strTrimSources() []struct{ name, src string } {
	var out []struct{ name, src string }
	for _, tc := range strTrimHeapCases {
		out = append(out, struct{ name, src string }{tc.name, strTrimHeap(tc.body, 4096)})
	}
	for _, tc := range strTrimFaultCases {
		out = append(out, struct{ name, src string }{tc.name, tc.src})
	}
	return out
}

func TestSelfHostStrTrimViewReleaseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strTrimSources() {
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
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, strTrimExitHint)
			}
		})
	}
}

func TestSelfHostStrTrimViewReleaseIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strTrimSources() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, strTrimExitHint)
			}
		})
	}
}

func TestSelfHostStrTrimViewReleaseWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping trim view release wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range strTrimSources() {
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
				t.Errorf("%s = %d, want 0 (%s)", tc.name, got, strTrimExitHint)
			}
		})
	}
}
