package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `buf_push_mapped(b, s, table)` and `buf_push_filtered(b, s, drop)` on the
// self-host IR path: the first appends each byte c of s as table[c], or
// unchanged when c is past the table's end (tr's SET1 -> SET2); the second
// appends each byte whose drop[c] is zero, or that is past the table's end
// (tr -d); `buf_push_expanded(b, s, table)` appends each byte's eight-byte
// record (cat -v / -T / -E).
//
// The table's length lives in the array header and its bytes in the
// backend's element slots, eight bytes on the register backends and four on
// wasm, so the sweep holds a full table, a short one and an empty one, over
// every length up to 40 and pushes that outgrow the builder.

// bufPushKernelsIRProg is SELF-CHECKING: it maps each string through a Fern
// reference and compares the builder's bytes against it. A failure returns a
// small distinct code saying which shape disagreed; 42 means every
// comparison matched.
const bufPushKernelsIRProg = `function ref(s: string, table: u8[]): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < s.len()) {
        var c: i32 = s[i] as i32;
        if (c < table.len()) { c = table[c] as i32; }
        out = out + chr(c);
        i = i + 1;
    }
    return out;
}
function ref_filtered(s: string, drop: u8[]): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < s.len()) {
        var c: i32 = s[i] as i32;
        if (c >= drop.len() || drop[c] as i32 == 0) { out = out + chr(c); }
        i = i + 1;
    }
    return out;
}
function alternate_table(): u8[] {
    var t: u8[] = __alloc_u8(256);
    var z: i32 = 0;
    while (z < 256) { t = t.with(z, (z % 2) as u8); z = z + 1; }
    return t;
}
function check_filtered(b: usize, held: string, s: string, drop: u8[]): boolean {
    buf_push(b, held);
    buf_push_filtered(b, s, drop);
    return buf_take(b) == held + ref_filtered(s, drop);
}
function ref_expanded(s: string, t: u8[]): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < s.len()) {
        var c: i32 = s[i] as i32;
        if (c * 8 + 8 > t.len()) {
            out = out + chr(c);
        } else {
            var n: i32 = t[c * 8] as i32;
            if (n > 7) { n = 7; }
            var k: i32 = 0;
            while (k < n) { out = out + chr(t[c * 8 + 1 + k] as i32); k = k + 1; }
        }
        i = i + 1;
    }
    return out;
}
// Byte c becomes c % 8 copies of 'a' + c % 26; every fifth record claims a
// length past seven, which counts as seven.
function expand_table(): u8[] {
    var t: u8[] = __alloc_u8(2048);
    var c: i32 = 0;
    while (c < 256) {
        var n: i32 = c % 8;
        if (c % 5 == 0) { n = 9 + c % 100; }
        t = t.with(c * 8, n as u8);
        var k: i32 = 1;
        while (k < 8) { t = t.with(c * 8 + k, (97 + c % 26) as u8); k = k + 1; }
        c = c + 1;
    }
    return t;
}
function check_expanded(b: usize, held: string, s: string, t: u8[]): boolean {
    buf_push(b, held);
    buf_push_expanded(b, s, t);
    return buf_take(b) == held + ref_expanded(s, t);
}
function rot_table(): u8[] {
    var t: u8[] = __alloc_u8(256);
    var z: i32 = 0;
    while (z < 256) { t = t.with(z, ((z + 13) % 256) as u8); z = z + 1; }
    return t;
}
function check(b: usize, held: string, s: string, table: u8[]): boolean {
    buf_push(b, held);
    buf_push_mapped(b, s, table);
    return buf_take(b) == held + ref(s, table);
}
function main(): i32 {
    var b: usize = buf_new(4);
    var rot: u8[] = rot_table();
    var short: u8[] = [120 as u8, 121 as u8, 122 as u8];
    var none: u8[] = [];
    var alt: u8[] = alternate_table();
    var ex: u8[] = expand_table();
    var n: i32 = 0;
    var s: string = "";
    while (n <= 40) {
        if (!check(b, "", s, rot)) { return 1; }
        if (!check(b, "ab", s, short)) { return 2; }
        if (!check(b, "", s, none)) { return 3; }
        if (!check_filtered(b, "", s, alt)) { return 7; }
        if (!check_filtered(b, "cd", s, short)) { return 8; }
        if (!check_filtered(b, "", s, none)) { return 9; }
        if (!check_filtered(b, "", s, rot)) { return 10; }
        if (!check_expanded(b, "ef", s, ex)) { return 12; }
        if (!check_expanded(b, "", s, short)) { return 13; }
        s = s + chr((n * 7 + 1) % 128);
        n = n + 1;
    }
    if (!check(b, "held", "\x00\x01\x02\x03", short)) { return 4; }
    if (!check(b, "", "\x00\x01\x02\x03", short) || buf_len(b) != 0) { return 5; }
    var big: string = "";
    var k: i32 = 0;
    while (k < 300) { big = big + "9z"; k = k + 1; }
    if (!check(b, "", big, rot)) { return 6; }
    if (!check_filtered(b, "", big, alt)) { return 11; }
    if (!check_expanded(b, "", big, ex)) { return 14; }
    var every: string = "";
    k = 0;
    while (k < 384) { every = every + chr(k % 128); k = k + 1; }
    if (!check_expanded(b, "gh", every, ex)) { return 15; }
    buf_free(b);
    return 42;
}
`

// runBufPushKernelsIR compiles bufPushKernelsIRProg with the self-host modload driver for
// the given register target and returns the exit code.
func runBufPushKernelsIR(t *testing.T, target string) int {
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

	progAsm, progDir := compileSourceModload(t, runner, driverBin, bufPushKernelsIRProg, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, linkGcc, progDir, "buf_push_kernels_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostBufPushKernelsIRX86_64(t *testing.T) {
	if got := runBufPushKernelsIR(t, "x86-64-linux"); got != 42 {
		t.Errorf("buf_push_mapped / buf_push_filtered self-host x86-64 = %d, want 42 (see bufPushKernelsIRProg for what each code means)", got)
	}
}

func TestSelfHostBufPushKernelsIRArm64(t *testing.T) {
	if got := runBufPushKernelsIR(t, "arm64-linux"); got != 42 {
		t.Errorf("buf_push_mapped / buf_push_filtered self-host arm64 = %d, want 42 (see bufPushKernelsIRProg for what each code means)", got)
	}
}

// TestSelfHostBufPushKernelsIRWasm runs the same program through the self-hosted
// wasm IR driver. The helper's presence in the emitted text is asserted, so a
// module that silently stopped needing it would fail rather than pass by
// exercising nothing.
func TestSelfHostBufPushKernelsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host buf_push kernels wasm IR e2e")
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
	cmd.Stdin = strings.NewReader(bufPushKernelsIRProg)
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("$__fern_buf_push_mapped")) || !bytes.Contains(wat, []byte("$__fern_buf_push_filtered")) || !bytes.Contains(wat, []byte("$__fern_buf_push_expanded")) {
		t.Fatal("emitted wat lacks one of the buf_push kernel helpers — the op did not lower")
	}
	watFile := filepath.Join(dir, "buf_push_kernels.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_, _ = run.CombinedOutput()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if got := run.ProcessState.ExitCode(); got != 42 {
		t.Errorf("buf_push_mapped / buf_push_filtered self-host wasm = %d, want 42 (see bufPushKernelsIRProg for what each code means)", got)
	}
}
