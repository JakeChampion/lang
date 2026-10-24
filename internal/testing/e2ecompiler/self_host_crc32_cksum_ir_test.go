package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const crc32CksumIRProg = `function ref(crc: i32, s: string): i32 {
    let c: i32 = crc;
    let i: i32 = 0;
    while (i < s.len()) {
        c = c ^ ((s[i] as i32) << 24);
        let k: i32 = 0;
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
    let n: i32 = 0;
    while (n <= 40) {
        let base: string = "";
        let k: i32 = 0;
        while (k < n) { base = base + "a"; k = k + 1; }
        if (__crc32_cksum(0, base) != ref(0, base)) { return 1; }
        if (__crc32_cksum(1, base) != ref(1, base)) { return 2; }
        if (__crc32_cksum(0 - 1, base) != ref(0 - 1, base)) { return 3; }
        let at: i32 = 0;
        while (at < n) {
            let s: string = slice_unchecked(base, 0, at) + string_from_bytes_unchecked([255 as u8]) + slice_unchecked(base, at + 1, n);
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
    // The top bit live in the RESULT, which a zero-extending push turns into
    // a large positive number instead. Both answers below have bit 31 set —
    // MOST do not, so the length is the whole of what makes these two cases
    // carry the claim: 40 high bytes answers 0x438e3ca6 and cannot tell the
    // two pushes apart at all. One block and eight.
    let hi: string = "";
    let h: i32 = 0;
    while (h < 16) { hi = hi + string_from_bytes_unchecked([255 as u8]); h = h + 1; }
    if (__crc32_cksum(0, hi) != 0 - 223276853) { return 10; }
    while (h < 128) { hi = hi + string_from_bytes_unchecked([255 as u8]); h = h + 1; }
    if (__crc32_cksum(0, hi) != 0 - 1955225115) { return 14; }
    // The top bit live in the INCOMING crc.
    if (__crc32_cksum(0 - 1, "x") != 0 - 2001448981) { return 11; }
    // The streaming identity the carried state exists for: the same bytes cut
    // at irregular offsets fold to the same CRC as one call.
    let full: string = "abcdefghijklmnopqrstuvwxyz0123456789";
    if (__crc32_cksum(0, full) != 0 - 1714478310) { return 12; }
    let a: i32 = __crc32_cksum(0, slice_unchecked(full, 0, 7));
    let b: i32 = __crc32_cksum(a, slice_unchecked(full, 7, 20));
    if (__crc32_cksum(b, slice_unchecked(full, 20, 36)) != 0 - 1714478310) { return 13; }
    return 42;
}
`

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

func TestSelfHostCrc32CksumIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host crc32_cksum wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
