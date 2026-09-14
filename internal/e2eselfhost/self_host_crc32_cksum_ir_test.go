package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__crc32_cksum(crc, s)` on the self-host IR path — the CRC-32 cksum(1)
// prints (poly 0x04C11DB7, MSB first, unreflected, no final complement),
// #9128.
//
// It lands SCALAR on all three self-host backends at once, which is §3.4's
// step 1: the intrinsic becomes total before anything depends on it being
// fast. The two native emitters fold the same bytes with a carry-less
// multiply, so what these three have to match is the DEFINITION.
//
// Two things a port of this one gets wrong that no sibling can teach it:
//
//   - The STRING IS OPERAND 1, not 0. The carried crc comes first, so a body
//     copied from count_byte unboxes the wrong stack slot. Every length in
//     the sweep below fails at once when that happens, which is the good
//     case; what is not is a body that pops in the right order and then
//     zero-extends the result, because the top bit is live here and a CRC
//     with it set reads back as a large positive number from a 64-bit slot.
//     The high-byte cases are what separate that.
//
//   - It CARRIES state in. sum_bytes starts from zero every call; this one
//     resumes, and that identity — one call over the whole string equals
//     three calls over its pieces — is the entire reason std/hash can hash a
//     stream in chunks. The reference below cannot check it, because a
//     reference implementation carries state the same way a broken kernel
//     that ignores the incoming crc would not. So the chunking is checked
//     against a FIXED expectation instead.

// crc32CksumIRProg is SELF-CHECKING: it carries its own bit-at-a-time
// reference in Fern and compares `__crc32_cksum` against it over every length
// to 40, then pins seven values computed by the Go oracle in
// internal/e2e/crc32_cksum_test.go so that a reference and a kernel wrong in
// the same way still fail.
//
// A failure returns a small distinct code rather than a checksum, so the exit
// status says WHICH shape disagreed. 42 means every comparison matched.
const crc32CksumIRProg = `function ref(crc: i32, s: string): i32 {
    var c: i32 = crc;
    var i: i32 = 0;
    while (i < s.len()) {
        c = c ^ ((s[i] as i32) << 24);
        var k: i32 = 0;
        while (k < 8) {
            if (c < 0) { c = (c << 1) ^ 0x04c11db7; }
            else { c = c << 1; }
            k = k + 1;
        }
        i = i + 1;
    }
    return c;
}
function main(): i32 {
    var n: i32 = 0;
    while (n <= 40) {
        var base: string = "";
        var k: i32 = 0;
        while (k < n) { base = base + "a"; k = k + 1; }
        if (__crc32_cksum(0, base) != ref(0, base)) { return 1; }
        if (__crc32_cksum(1, base) != ref(1, base)) { return 2; }
        if (__crc32_cksum(0 - 1, base) != ref(0 - 1, base)) { return 3; }
        var at: i32 = 0;
        while (at < n) {
            var s: string = slice_unchecked(base, 0, at) + "\xff" + slice_unchecked(base, at + 1, n);
            if (__crc32_cksum(0, s) != ref(0, s)) { return 4; }
            if (__crc32_cksum(0 - 559038737, s) != ref(0 - 559038737, s)) { return 5; }
            at = at + 1;
        }
        n = n + 1;
    }
    // Fixed expectations from the Go oracle, so a reference and a kernel
    // wrong in the same way cannot both pass.
    if (__crc32_cksum(0, "") != 0) { return 6; }
    if (__crc32_cksum(0, "a") != 0 - 1469785312) { return 7; }
    if (__crc32_cksum(0, "hello world") != 1937437358) { return 8; }
    if (__crc32_cksum(0, "abcdefghijklmnopqrstuvwxyz") != 1002611811) { return 9; }
    // The top bit live in the RESULT: 40 high bytes, which a zero-extending
    // push turns into a large positive number instead.
    var hi: string = "";
    var h: i32 = 0;
    while (h < 40) { hi = hi + "\xff"; h = h + 1; }
    if (__crc32_cksum(0, hi) != 1133395110) { return 10; }
    // The top bit live in the INCOMING crc.
    if (__crc32_cksum(0 - 1, "x") != 0 - 2001448981) { return 11; }
    // The streaming identity the carried state exists for: the same bytes cut
    // at irregular offsets fold to the same CRC as one call.
    var full: string = "abcdefghijklmnopqrstuvwxyz0123456789";
    if (__crc32_cksum(0, full) != 0 - 1714478310) { return 12; }
    var a: i32 = __crc32_cksum(0, slice_unchecked(full, 0, 7));
    var b: i32 = __crc32_cksum(a, slice_unchecked(full, 7, 20));
    if (__crc32_cksum(b, slice_unchecked(full, 20, 36)) != 0 - 1714478310) { return 13; }
    return 42;
}
`

// runCrc32CksumIR compiles crc32CksumIRProg with the self-host modload driver
// for the given register target and returns the exit code.
func runCrc32CksumIR(t *testing.T, target string) int {
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

	progAsm, progDir := compileSourceModload(t, runner, driverBin, crc32CksumIRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "crc32_cksum_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostCrc32CksumIRX86_64(t *testing.T) {
	if got := runCrc32CksumIR(t, "x86-64-linux"); got != 42 {
		t.Errorf("__crc32_cksum self-host x86-64 = %d, want 42 (see crc32CksumIRProg for what each code means)", got)
	}
}

func TestSelfHostCrc32CksumIRArm64(t *testing.T) {
	if got := runCrc32CksumIR(t, "arm64-linux"); got != 42 {
		t.Errorf("__crc32_cksum self-host arm64 = %d, want 42 (see crc32CksumIRProg for what each code means)", got)
	}
}

// TestSelfHostCrc32CksumIRWasm runs the same program through the self-hosted
// wasm IR driver. The helper's presence in the emitted text is asserted, so a
// module that silently stopped needing it would fail rather than pass by
// exercising nothing.
func TestSelfHostCrc32CksumIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host crc32_cksum wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = strings.NewReader(crc32CksumIRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("$__fern_crc32_cksum")) {
		t.Fatal("emitted wat has no $__fern_crc32_cksum helper — the op did not lower")
	}
	watFile := filepath.Join(dir, "crc32_cksum.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != 42 {
		t.Errorf("__crc32_cksum self-host wasm = %d, want 42 (see crc32CksumIRProg for what each code means)", code)
	}
}
