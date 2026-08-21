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

// `var r: string = base.replace(old, new)` leaked its box whenever the needle
// was present, and could not simply be credited the way `.trim()` was in #7249,
// because replace has a genuine identity fast-path.
//
// Measured directly, one call each, bytes allocated:
//
//	needle ABSENT    x86-64 0     wasm 0     -> the receiver's own box comes back
//	needle PRESENT   x86-64 136   wasm 120   -> a fresh box
//
// So the excluded-forms comment in str_local_binding_is_fresh is right about
// replace in a way it was not about trim: the result really can BE the receiver.
// An unguarded release frees a box the receiver still owns, and the receiver's
// own release then double-frees.
//
// The analysis cannot settle which case it is — the needle is a run-time value —
// so the credit says "this box MAY be ours" and a guard settles it where the
// answer exists. `emit_str_slot_release` frees under `result != receiver`, the
// same cow test `emit_str_reclaim_store` already uses, pointed at the receiver
// slot that `LocalInfo.str_identity_src` records at the binding site.
//
// Measured, 400 rounds of the harness below, a pair of compilers from the same
// commit:
//
//	shape                       x86-64          wasm
//	replace, needle present   54400 -> 0    48000 -> 0
//	replace, needle absent        0 -> 0         0 -> 0
//
// The guard is witnessed at FAULT level, not merely by the absent-case staying
// at zero: a compiler with the credit and the guard removed over-releases on the
// liveness case below — exit 99 (rc underflow) on x86-64, a trap on wasm —
// against a clean main and a clean fix.

const strReplacePrelude = `function w(pre: string): string { return pre + "-a-wide-payload-past-any-inline-threshold-and-well-past-the-box-so-the-source-dominates-0123456789"; }
`

func strReplaceHeap(body string, limit int) string {
	return strReplacePrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
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

var strReplaceHeapCases = []struct {
	name string
	body string
}{
	{"str-replace-fresh-released", `    var r: string = base.replace("wide", "NARROW");
    return r.len() % 251;`},
	// The identity case allocates nothing to begin with, so this pins that the
	// guard did not somehow ADD a cost — and, with the fault case below, that it
	// skipped the free rather than performing a harmless one.
	{"str-replace-identity-stays-flat", `    var r: string = base.replace("ZZZZ", "!");
    return r.len() % 251;`},
}

var strReplaceFaultCases = []struct {
	name string
	src  string
}{
	// THE case. `same` IS base's own box; `diff` is fresh. Both are credited, and
	// only one may be freed. A compiler with the guard removed exits 99 here on
	// x86-64 and traps on wasm.
	{"str-replace-identity-and-fresh-live", strReplacePrelude + `function round(pre: string): i32 {
    var base: string = w(pre);
    var same: string = base.replace("ZZZZ", "!");
    var diff: string = base.replace("wide", "NARROW");
    var p1: string = w("XXXXXXXX");
    var p2: string = w("YYYYYYYY");
    if (p1.len() + p2.len() < 0) { return 0; }
    if (!base.starts_with("abcdefgh-a-wide")) { return 0 - 1; }
    if (!same.starts_with("abcdefgh-a-wide")) { return 0 - 2; }
    if (base.index_of("XXXX") >= 0) { return 0 - 3; }
    if (same.len() != base.len()) { return 0 - 4; }
    if (diff.index_of("NARROW") < 0) { return 0 - 5; }
    if (diff.len() != base.len() + 2) { return 0 - 6; }
    return 3;
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; while (i < 2000) { if (round(pre) != 3) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`},
	// ESCAPE: the result is returned, so the credit is withheld.
	{"str-replace-escapes-return", strReplacePrelude + `function rep(pre: string): string { var base: string = w(pre); var r: string = base.replace("wide", "NARROW"); return r; }
function round(pre: string): i32 {
    var r: string = rep(pre);
    var p1: string = w("XXXXXXXX");
    if (p1.len() < 0) { return 0; }
    if (r.index_of("NARROW") < 0) { return 0 - 1; }
    return r.len() % 251;
}
function main(): i32 { var pre: string = "abcdefgh"; var i: i32 = 0; var want: i32 = round(pre); while (i < 2000) { if (round(pre) != want) { return 97; } i = i + 1; } if (__rc_underflow() != 0) { return 99; } return 0; }`},
	// A USER `.replace()` returning a field alias: refused by the declared-type
	// receiver test, like its trim / join / to_string siblings.
	{"str-replace-user-method-not-credited", strReplacePrelude + `struct Holder { name: string, tag: string }
function (h: Holder) replace(a: string, b: string): string { return h.name; }
function rep(h: Holder): i32 { var r: string = h.replace("x", "y"); return r.len() % 251; }
function churn(pre: string): i32 { var a: string = w(pre + "1"); var b: string = w(pre + "2"); return a.len() + b.len(); }
function main(): i32 {
    var keep: Holder = Holder { name: w("aaaa"), tag: w("bbbb") };
    var i: i32 = 0;
    while (i < 2000) {
        if (rep(keep) < 0) { return 96; }
        if (churn("QQQQQQQQ") < 0) { return 95; }
        if (!keep.name.starts_with("aaaa-")) { return 97; }
        if (!keep.tag.starts_with("bbbb-")) { return 97; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`},
}

const strReplaceExitHint = "98 = the fresh box was stranded; 99 = over-release (the identity guard); 97 = value corrupted; 96/95 = the probe's own guards"

func strReplaceSources() []struct{ name, src string } {
	var out []struct{ name, src string }
	for _, tc := range strReplaceHeapCases {
		out = append(out, struct{ name, src string }{tc.name, strReplaceHeap(tc.body, 4096)})
	}
	for _, tc := range strReplaceFaultCases {
		out = append(out, struct{ name, src string }{tc.name, tc.src})
	}
	return out
}

// TestSelfHostStrReplaceIdentityIRX86_64 is the x86-64 leg.
func TestSelfHostStrReplaceIdentityIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strReplaceSources() {
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
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, strReplaceExitHint)
			}
		})
	}
}

// TestSelfHostStrReplaceIdentityIRArm64 is the arm64 leg.
func TestSelfHostStrReplaceIdentityIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range strReplaceSources() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s = %d, want 0 (%s)", tc.name, code, strReplaceExitHint)
			}
		})
	}
}

// TestSelfHostStrReplaceIdentityWasmIR is the wasm leg.
func TestSelfHostStrReplaceIdentityWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping replace identity wasm IR e2e")
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

	for _, tc := range strReplaceSources() {
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
				t.Errorf("%s = %d, want 0 (%s)", tc.name, got, strReplaceExitHint)
			}
		})
	}
}
