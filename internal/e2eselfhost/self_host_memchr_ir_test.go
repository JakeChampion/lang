package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__memchr(s, byte, from)` on the self-host IR path — the first fused SIMD
// kernel of docs/ATLAS-PLATFORM-PLAN.md §3, completed across the three
// self-host backends.
//
// §3.4 orders the work as "total everywhere before fast anywhere": the native
// backends serve this op with 16-byte-per-iteration vector kernels (SSE2 /
// NEON / v128), and the self-host backends serve the same meaning a byte at a
// time. That asymmetry is deliberate, and it is also what these tests are for
// — the two tiers must agree on every answer, which is the property a later
// slice will need when it swaps the scalar bodies for vector ones.
//
// Only once all six lowerings exist may `std/string` route its single-byte
// search through the intrinsic: the self-hosted compiler compiles the stdlib,
// the AST emitters are gone, and every backend routes IR-or-error
// (docs/SELFHOST-AST-RETIREMENT.md), so a missing lowering is a hard compile
// error rather than a slow path.

// memchrIRProg is SELF-CHECKING: it carries its own reference implementation
// written in Fern and compares `__memchr` against it, so the corpus can be
// swept exhaustively without a Go-side expectation list to keep in step.
//
// The sweep runs length 0..40 with the needle at every position and the scan
// starting before / at / after it. That range covers two full 16-byte vector
// blocks plus a partial tail on either side — the boundaries these bodies do
// not have yet, but the ones they must keep answering correctly when they do.
//
// A failure returns a small distinct code rather than a count, so the exit
// status says WHICH shape disagreed. 42 means every comparison matched.
const memchrIRProg = `function ref(s: string, b: i32, from: i32): i32 {
    if (b < 0) { return 0 - 1; }
    if (b > 255) { return 0 - 1; }
    var i: i32 = from;
    if (i < 0) { i = 0; }
    while (i < s.len()) {
        if ((s[i] as i32) == b) { return i; }
        i = i + 1;
    }
    return 0 - 1;
}
function main(): i32 {
    var n: i32 = 0;
    while (n <= 40) {
        var base: string = "";
        var k: i32 = 0;
        while (k < n) { base = base + "a"; k = k + 1; }
        if (__memchr(base, 122, 0) != ref(base, 122, 0)) { return 1; }
        var at: i32 = 0;
        while (at < n) {
            var s: string = base[0:at] + "z" + base[at + 1:n];
            if (__memchr(s, 122, 0) != ref(s, 122, 0)) { return 2; }
            if (__memchr(s, 122, at) != ref(s, 122, at)) { return 3; }
            if (__memchr(s, 122, at + 1) != ref(s, 122, at + 1)) { return 4; }
            if (__memchr(s, 97, at) != ref(s, 97, at)) { return 5; }
            at = at + 1;
        }
        n = n + 1;
    }
    if (__memchr("abc", 97, 100) != 0 - 1) { return 6; }
    if (__memchr("abc", 99, 0 - 5) != 2) { return 7; }
    if (__memchr("abc", 256, 0) != 0 - 1) { return 8; }
    if (__memchr("abc", 0 - 1, 0) != 0 - 1) { return 9; }
    if (__memchr("", 97, 0) != 0 - 1) { return 10; }
    return 42;
}
`

// runMemchrIR compiles memchrIRProg with the self-host modload driver for the
// given register target and returns the exit code.
func runMemchrIR(t *testing.T, target string) int {
	t.Helper()
	var runner, runPrefix, extra []string
	var driverBin, linkGcc string
	if target == "arm64" {
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

	progAsm, progDir := compileSourceModload(t, runner, driverBin, memchrIRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "memchr_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostMemchrIRX86_64(t *testing.T) {
	if got := runMemchrIR(t, "x86-64"); got != 42 {
		t.Errorf("__memchr self-host x86-64 = %d, want 42 (see memchrIRProg for what each code means)", got)
	}
}

func TestSelfHostMemchrIRArm64(t *testing.T) {
	if got := runMemchrIR(t, "arm64"); got != 42 {
		t.Errorf("__memchr self-host arm64 = %d, want 42 (see memchrIRProg for what each code means)", got)
	}
}

// TestSelfHostMemchrIRWasm runs the same program through the self-hosted wasm
// IR driver. The self-host wasm string is a single `[len@0][bytes@4]` block —
// not the native wasm backend's two-word SSO pair — so the byte address is
// unconditional here and there is no inline-string branch to get wrong. That
// difference is why this leg is not redundant with the two above.
//
// The op lowers to a call rather than inline code: the body needs a loop over
// its own locals, and the operand stack here is the wasm value stack. The
// helper's presence in the emitted text is asserted, so a module that silently
// stopped needing it would fail rather than pass by not exercising anything.
func TestSelfHostMemchrIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host memchr wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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
	cmd.Stdin = strings.NewReader(memchrIRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("$__fern_memchr")) {
		t.Fatal("emitted wat has no $__fern_memchr helper — the op did not lower")
	}
	watFile := filepath.Join(dir, "memchr.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != 42 {
		t.Errorf("__memchr self-host wasm = %d, want 42 (see memchrIRProg for what each code means)", code)
	}
}
