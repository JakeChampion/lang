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

// An f-string interpolating a CALL stranded that call's box, once per evaluation,
// while the same call written as an explicit concat operand did not.
//
// parser.fern desugars every interpolant to `(<expr>).to_string()`. On a STRING
// receiver that is the receiver-identity fast-path, which `str_local_binding_is_
// fresh` excludes by design: the result IS the receiver, so freeing it would
// release a box the source still owns.
//
// That reasoning holds for a NAMED receiver and inverts for an ANONYMOUS one.
// When the receiver is a call the whole-program proof says returns a fresh
// sole-owned box, the identity result is that box, this frame is the only thing
// holding it, and NOT freeing it is the leak.
//
// Isolated one shape at a time, which is what located the desugar rather than the
// f-string machinery — the explicit spelling leaks identically:
//
//	shape                        x86-64        arm64         wasm
//	f"{w(pre)}"               54400 -> 0    54400 -> 0    48000 -> 0
//	f"{w(pre)}-{w(pre)}"     108800 -> 0   108800 -> 0    96000 -> 0
//	w(pre).to_string()        54400 -> 0    54400 -> 0    48000 -> 0
//	f"{pre}"                       0             0             0
//	f"n={n}"                       0             0             0
//	pre + "-" + w(pre)             0             0             0
//
// The last three are the controls that say what this is NOT: a bare-ident
// interpolant is a true identity of a live local and must stay uncredited, a
// scalar one was already credited (#6599), and the concat spelling already
// reclaimed its operand — which is what made the f-string case look like a
// machinery bug until the explicit `.to_string()` reproduced it exactly.

const interpCallPrelude = `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
`

func interpCallHeap(body string, limit int) string {
	return interpCallPrelude + `function round(pre: string): i32 {
` + body + `
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

var interpCallHeapCases = []struct {
	name string
	body string
}{
	{"interp-call-released", `    var s: string = f"{w(pre)}";
    return s.len() % 251;`},
	// Two interpolants: the parent leaks exactly twice as much, so the credit has
	// to fire per interpolant rather than per f-string.
	{"interp-two-calls-released", `    var s: string = f"{w(pre)}-{w(pre)}";
    return s.len() % 251;`},
	// The desugar written out by hand. It leaks the same on the parent, which is
	// what says the bug is the `.to_string()` rule and not the f-string path.
	{"interp-explicit-tostring-released", `    var s: string = w(pre).to_string();
    return s.len() % 251;`},
	// CONTROLS, all already 0 — here so a future widening of the credit that
	// swallowed one of them would show up as an over-release below rather than as
	// a silent change of class.
	{"interp-bare-ident-control", `    var base: string = w(pre);
    var s: string = f"{base}";
    return (s.len() + base.len()) % 251;`},
	{"interp-scalar-control", `    var n: i32 = pre.len();
    var s: string = f"n={n}";
    return s.len() % 251;`},
	{"interp-explicit-concat-control", `    var s: string = pre + "-" + w(pre);
    return s.len() % 251;`},
}

var interpCallFaultCases = []struct {
	name string
	src  string
}{
	// LIVENESS, holding all three classes at once: a named-receiver identity
	// (uncredited, and its source must survive), a fresh-temp identity (credited,
	// and its value must still read), and an f-string mixing a call with a live
	// local. Decoys sit between the binds and the reads.
	{"interp-classes-live", interpCallPrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var named: string = base.to_string();
    var fresh: string = w(pre).to_string();
    var interp: string = f"{w(pre)}|{base}";
    var p1: string = w("XXXXXXXX");
    var p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (!base.starts_with("abcdefgh-")) { return 0 - 1; }
    if (!named.starts_with("abcdefgh-")) { return 0 - 2; }
    if (!fresh.starts_with("abcdefgh-")) { return 0 - 3; }
    if (!interp.starts_with("abcdefgh-")) { return 0 - 4; }
    if (base.index_of("XXXX") >= 0) { return 0 - 5; }
    if (named.len() != base.len()) { return 0 - 6; }
    if (interp.index_of("|") < 0) { return 0 - 7; }
    return 3;
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { if (round(pre) != 3) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`},
	// A USER `to_string` returning a field ALIAS, on a struct local AND on a
	// fresh-ret struct temp. Neither may be credited: the receiver being fresh
	// says nothing about whether the RESULT is fresh once a user method is in the
	// way. `mk` returns a struct, so it is not in the string fresh-ret registry —
	// this case is what proves that rather than assuming it.
	{"interp-user-tostring-not-credited", interpCallPrelude + `struct Holder { name: string, tag: string }
function mk(pre: string): Holder { return Holder { name: w(pre), tag: w(pre + "t") }; }
function (h: Holder) to_string(): string { return h.name; }
function round(pre: string): i32 {
    var h: Holder = mk(pre);
    var s: string = h.to_string();
    var t: string = mk(pre).to_string();
    var p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (!s.starts_with("abcdefgh-")) { return 0 - 1; }
    if (!t.starts_with("abcdefgh-")) { return 0 - 2; }
    if (!h.name.starts_with("abcdefgh-")) { return 0 - 3; }
    if (s.index_of("XXXX") >= 0) { return 0 - 4; }
    return 3;
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { if (round(pre) != 3) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`},
	// ESCAPE: the credited local is returned, so it must not be freed.
	{"interp-call-escape-return", interpCallPrelude + `function mk(pre: string): string { var s: string = f"{w(pre)}"; return s; }
function round(pre: string): i32 {
    var s: string = mk(pre);
    var p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (!s.starts_with("abcdefgh-")) { return 0 - 1; }
    return s.len() % 251;
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; var want: i32 = round(pre); while (i < 2000) { if (round(pre) != want) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`},
}

const interpCallExitHint = "98 = the interpolated call's box was stranded; 99 = over-release; 97 = value corrupted"

func interpCallSources() []struct{ name, src string } {
	var out []struct{ name, src string }
	for _, tc := range interpCallHeapCases {
		out = append(out, struct{ name, src string }{tc.name, interpCallHeap(tc.body, 4096)})
	}
	for _, tc := range interpCallFaultCases {
		out = append(out, struct{ name, src string }{tc.name, tc.src})
	}
	return out
}

// TestSelfHostInterpCallTempIRX86_64 is the x86-64 leg.
func TestSelfHostInterpCallTempIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range interpCallSources() {
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
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, interpCallExitHint)
			}
		})
	}
}

// TestSelfHostInterpCallTempIRArm64 is the arm64 leg.
func TestSelfHostInterpCallTempIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range interpCallSources() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, interpCallExitHint)
			}
		})
	}
}

// TestSelfHostInterpCallTempWasmIR is the wasm leg.
func TestSelfHostInterpCallTempWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping interpolated-call temp wasm IR e2e")
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

	for _, tc := range interpCallSources() {
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
				t.Errorf("%s = %d, want 0 (%s)", tc.name, got, interpCallExitHint)
			}
		})
	}
}
