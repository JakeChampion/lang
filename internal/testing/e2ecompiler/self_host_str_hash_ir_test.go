package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `__str_hash(s, seed)` on the self-host IR path: the seeded word-at-a-time
// string hash (compiler/ir.fern's str_hash has the definition).

// strHashIRProg is SELF-CHECKING: it carries a Fern reference that assembles
// each word a byte at a time and compares the kernel against it over every
// length to 40 from several seeds, over 300 high bytes, and against eight
// values an independent implementation computed, so the kernel and the
// reference cannot drift together. 42 means every comparison matched.
const strHashIRProg = `function word(s: string, at: i32, n: i32): i64 {
    let w: i64 = 0i64;
    let k: i32 = n - 1;
    while (k >= 0) {
        w = w << 8i64 | s[at + k] as i64;
        k = k - 1;
    }
    return w;
}
function ref(s: string, seed: i32): i32 {
    let n: i32 = s.len();
    let h: i64 = -3750763034362895579i64 ^ seed as i64 ^ n as i64;
    let i: i32 = 0;
    while (i + 8 <= n) {
        h = (h ^ word(s, i, 8)) * 1099511628211i64;
        i = i + 8;
    }
    if (i < n) {
        if (n >= 8) {
            h = (h ^ word(s, n - 8, 8)) * 1099511628211i64;
        } else {
            h = (h ^ word(s, 0, n)) * 1099511628211i64;
        }
    }
    h = h ^ (h as u64 >> 32u64) as i64;
    let lo: i64 = h & 4294967295i64;
    if (lo >= 2147483648i64) {
        return (lo - 4294967296i64) as i32;
    }
    return lo as i32;
}
function main(): i32 {
    let n: i32 = 0;
    let s: string = "";
    while (n <= 40) {
        if (__str_hash(s, 0) != ref(s, 0)) { return 1; }
        if (__str_hash(s, 1) != ref(s, 1)) { return 2; }
        if (__str_hash(s, 0 - 1) != ref(s, 0 - 1)) { return 3; }
        if (__str_hash(s, 208357) != ref(s, 208357)) { return 4; }
        s = s + chr((n * 37 + 11) % 128);
        n = n + 1;
    }
    let high: string = "";
    let k: i32 = 0;
    while (k < 300) { high = high + "\xff\x80"; k = k + 1; }
    if (__str_hash(high, 4660) != ref(high, 4660)) { return 5; }
    let bytes: string = "\xc8\xc9\xca\xcb\xcc\xcd\xce\xcf\xd0\xd1\xd2\xd3\xd4\xd5\xd6\xd7\xd8\xd9\xda\xdb\xdc\xdd\xde\xdf\xe0\xe1\xe2\xe3\xe4\xe5\xe6\xe7\xe8\xe9\xea\xeb\xec\xed\xee\xef\xf0\xf1\xf2\xf3\xf4\xf5\xf6\xf7\xf8\xf9\xfa\xfb\xfc\xfd\xfe\xff";
    if (__str_hash("", 0) != 1339080641) { return 10; }
    if (__str_hash("a", 0) != 694301555) { return 11; }
    if (__str_hash("abcdefgh", 0) != 939000468) { return 12; }
    if (__str_hash("abcdefghi", 0) != 0 - 2056207145) { return 13; }
    if (__str_hash("hello, world", 7) != 0 - 196944044) { return 14; }
    if (__str_hash("hello, world", 0 - 1) != 0 - 194608413) { return 15; }
    if (__str_hash("__fn_ssa_lift__lift_impl", 208357) != 0 - 43142800) { return 16; }
    if (__str_hash(bytes, 0 - 123456) != 0 - 1417042185) { return 17; }
    return 42;
}
`

// runStrHashIR compiles strHashIRProg with the self-host modload driver for
// the given register target and returns the exit code.
func runStrHashIR(t *testing.T, target string) int {
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

	progAsm, progDir := compileSourceModload(t, runner, driverBin, strHashIRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "str_hash_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostStrHashIRX86_64(t *testing.T) {
	if got := runStrHashIR(t, "x86-64-linux"); got != 42 {
		t.Errorf("__str_hash self-host x86-64 = %d, want 42 (see strHashIRProg for what each code means)", got)
	}
}

func TestSelfHostStrHashIRArm64(t *testing.T) {
	if got := runStrHashIR(t, "arm64-linux"); got != 42 {
		t.Errorf("__str_hash self-host arm64 = %d, want 42 (see strHashIRProg for what each code means)", got)
	}
}

// TestSelfHostStrHashIRWasm runs the same program through the self-hosted
// wasm IR driver. The helper's presence in the emitted text is asserted, so a
// module that silently stopped needing it would fail rather than pass by
// exercising nothing.
func TestSelfHostStrHashIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host str_hash wasm IR e2e")
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
	cmd.Stdin = strings.NewReader(strHashIRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("$__fern_str_hash")) {
		t.Fatal("emitted wat has no $__fern_str_hash helper — the op did not lower")
	}
	watFile := filepath.Join(dir, "str_hash.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_, _ = run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if got := run.ProcessState.ExitCode(); got != 42 {
		t.Errorf("__str_hash self-host wasm = %d, want 42 (see strHashIRProg for what each code means)", got)
	}
}
