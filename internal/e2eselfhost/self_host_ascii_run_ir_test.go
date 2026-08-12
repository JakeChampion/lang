package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__ascii_run(s, from)` on the self-host IR path — the second fused SIMD
// kernel of docs/ATLAS-PLATFORM-PLAN.md §3, completed across the three
// self-host backends.
//
// Same §3.4 ordering as __memchr's sibling suite: the native backends serve
// this op with 16-byte-per-iteration vector kernels and the self-host backends
// serve the same meaning a byte at a time, so the two tiers must agree on every
// answer. What differs is that this op is being made TOTAL BEFORE any caller
// adopts it, rather than after — __memchr was adopted with one backend
// (arm64-ssa) still missing an entry, and CI reported it as a link error.
//
// The contract difference worth stating once more, because it is what a reader
// porting the op to an eighth backend gets wrong: a miss returns the LENGTH,
// not -1. `i = __ascii_run(s, i)` is meant to be a branch-free skip.

// asciiRunIRProg is SELF-CHECKING: it carries a Fern reference implementation
// and compares `__ascii_run` against it, so the corpus sweeps exhaustively with
// no Go-side expectation list to keep in step.
//
// The sweep runs length 0..40 with the high byte at every position and the scan
// starting before / at / after it — two full 16-byte vector blocks plus a
// partial tail either side, which are the boundaries these scalar bodies do not
// have yet but must keep answering correctly when they get them.
//
// A failure returns a small distinct code rather than a count, so the exit
// status says WHICH shape disagreed. 42 means every comparison matched.
const asciiRunIRProg = `function ref(s: string, from: i32): i32 {
    var i: i32 = from;
    if (i < 0) { i = 0; }
    while (i < s.len()) {
        if ((s[i] as i32) >= 128) { return i; }
        i = i + 1;
    }
    return s.len();
}
function main(): i32 {
    var n: i32 = 0;
    while (n <= 40) {
        var base: string = "";
        var k: i32 = 0;
        while (k < n) { base = base + "a"; k = k + 1; }
        if (__ascii_run(base, 0) != ref(base, 0)) { return 1; }
        var at: i32 = 0;
        while (at < n) {
            var s: string = base[0:at] + "\xc3" + base[at + 1:n];
            if (__ascii_run(s, 0) != ref(s, 0)) { return 2; }
            if (__ascii_run(s, at) != ref(s, at)) { return 3; }
            if (__ascii_run(s, at + 1) != ref(s, at + 1)) { return 4; }
            at = at + 1;
        }
        n = n + 1;
    }
    // Every byte >= 0x80 is a hit, not just UTF-8 lead bytes: 0x80 is a bare
    // continuation and 0xFF is not legal UTF-8 at all. This intrinsic answers
    // "not ASCII", not "start of a codepoint".
    if (__ascii_run("abc\x80def", 0) != 3) { return 5; }
    if (__ascii_run("abc\xffdef", 0) != 3) { return 6; }
    if (__ascii_run("abc\xbfdef", 0) != 3) { return 7; }
    // 0x7F is the last ASCII byte and must NOT be a hit — the off-by-one an
    // inclusive/exclusive slip on the high-bit test produces.
    if (__ascii_run("\x7f\x7f\x7f", 0) != 3) { return 8; }
    // The miss value is the length, on every shape that can miss.
    if (__ascii_run("", 0) != 0) { return 9; }
    if (__ascii_run("abc", 100) != 3) { return 10; }
    if (__ascii_run("abc", 0 - 5) != 3) { return 11; }
    // A hit at index 0, and one reached only after a whole vector block.
    if (__ascii_run("\xffabc", 0) != 0) { return 12; }
    if (__ascii_run("aaaaaaaaaaaaaaaa\xc3", 0) != 16) { return 13; }
    return 42;
}
`

// runAsciiRunIR compiles asciiRunIRProg with the self-host modload driver for
// the given register target and returns the exit code.
func runAsciiRunIR(t *testing.T, target string) int {
	t.Helper()
	var runner, runPrefix, extra []string
	var driverBin, linkGcc string
	if target == "arm64-linux" {
		var qemu string
		_, runner, driverBin = buildModloadArm64DriverX86(t)
		linkGcc, qemu = arm64Tooling(t)
		if qemu != "" {
			runPrefix = []string{qemu}
		}
		extra = []string{"-target", "arm64-linux"}
	} else {
		linkGcc, runner, driverBin = buildModloadDriverX86(t)
		runPrefix = runner
	}

	progAsm, progDir := compileSourceModload(t, runner, driverBin, asciiRunIRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "ascii_run_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostAsciiRunIRX86_64(t *testing.T) {
	if got := runAsciiRunIR(t, "x86-64-linux"); got != 42 {
		t.Errorf("__ascii_run self-host x86-64 = %d, want 42 (see asciiRunIRProg for what each code means)", got)
	}
}

func TestSelfHostAsciiRunIRArm64(t *testing.T) {
	if got := runAsciiRunIR(t, "arm64-linux"); got != 42 {
		t.Errorf("__ascii_run self-host arm64 = %d, want 42 (see asciiRunIRProg for what each code means)", got)
	}
}

// TestSelfHostAsciiRunIRWasm runs the same program through the self-hosted wasm
// IR driver. The self-host wasm string is a single `[len@0][bytes@4]` block —
// not the native wasm backend's two-word SSO pair — so the byte address is
// unconditional and there is no inline-string branch to get wrong. That
// difference is why this leg is not redundant with the two above.
//
// The helper's presence in the emitted text is asserted, so a module that
// silently stopped needing it would fail rather than pass by exercising
// nothing.
func TestSelfHostAsciiRunIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host ascii_run wasm IR e2e")
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

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = strings.NewReader(asciiRunIRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("$__fern_ascii_run")) {
		t.Fatal("emitted wat has no $__fern_ascii_run helper — the op did not lower")
	}
	watFile := filepath.Join(dir, "ascii_run.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != 42 {
		t.Errorf("__ascii_run self-host wasm = %d, want 42 (see asciiRunIRProg for what each code means)", code)
	}
}
