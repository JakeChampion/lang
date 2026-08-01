package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostUuidIR covers std/uuid's v4 + v7 generators through the
// self-hosted x86-64 compiler's IR path (the "self-host pending" audit gap for
// std/uuid, docs/FEATURE-AUDIT.md). Both compile via the IR path now that the
// random_bytes byte source, u32 / cast, i64 (uuid_v7's ms timestamp), and
// string slice+concat lowerings are in place — the std/uuid header comment's
// design intent ("uuid_v4 lowers through the self-hosted compiler's IR path on
// every backend"). random_bytes is a CSPRNG, so the output isn't deterministic;
// the program self-validates the canonical structure (length 36, hyphens at
// 8/13/18/23, the version nibble, and an RFC-4122 variant nibble in 8/9/a/b) and
// returns 42 on success — exactly the shape the native audit_std_uuid checks.
func TestSelfHostUuidIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emitAndRunIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s", err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
	}

	// std/uuid (inlined; the self-host driver reads one program — random_bytes /
	// now_unix_ms are builtins, no imports). uuid_hex_digit / uuid_byte_hex match
	// the stdlib verbatim.
	const helpers = `
function uuid_hex_digit(n: i32): string { return "0123456789abcdef"[n : n + 1]; }
function uuid_byte_hex(b: i32): string { return uuid_hex_digit((b >> 4) & 15) + uuid_hex_digit(b & 15); }
function uuid_v4(): string {
    var b: string = random_bytes(16);
    var b6: i32 = (b[6] & 15) | 64;
    var b8: i32 = (b[8] & 63) | 128;
    return uuid_byte_hex(b[0]) + uuid_byte_hex(b[1]) + uuid_byte_hex(b[2]) + uuid_byte_hex(b[3])
        + "-" + uuid_byte_hex(b[4]) + uuid_byte_hex(b[5])
        + "-" + uuid_byte_hex(b6) + uuid_byte_hex(b[7])
        + "-" + uuid_byte_hex(b8) + uuid_byte_hex(b[9])
        + "-" + uuid_byte_hex(b[10]) + uuid_byte_hex(b[11]) + uuid_byte_hex(b[12])
        + uuid_byte_hex(b[13]) + uuid_byte_hex(b[14]) + uuid_byte_hex(b[15]);
}
function uuid_v7(): string {
    var ms: i64 = now_unix_ms();
    var r: string = random_bytes(10);
    var t0: i32 = ((ms >> 40) & 255) as i32; var t1: i32 = ((ms >> 32) & 255) as i32;
    var t2: i32 = ((ms >> 24) & 255) as i32; var t3: i32 = ((ms >> 16) & 255) as i32;
    var t4: i32 = ((ms >> 8) & 255) as i32; var t5: i32 = (ms & 255) as i32;
    var b6: i32 = (r[0] & 15) | 112; var b8: i32 = (r[2] & 63) | 128;
    return uuid_byte_hex(t0) + uuid_byte_hex(t1) + uuid_byte_hex(t2) + uuid_byte_hex(t3)
        + "-" + uuid_byte_hex(t4) + uuid_byte_hex(t5)
        + "-" + uuid_byte_hex(b6) + uuid_byte_hex(r[1])
        + "-" + uuid_byte_hex(b8) + uuid_byte_hex(r[3])
        + "-" + uuid_byte_hex(r[4]) + uuid_byte_hex(r[5]) + uuid_byte_hex(r[6])
        + uuid_byte_hex(r[7]) + uuid_byte_hex(r[8]) + uuid_byte_hex(r[9]);
}
`
	// validate(u, ver) returns 42 iff u is the canonical hyphenated form with the
	// expected version char and a 8/9/a/b variant nibble; else a distinct code.
	const validate = `
function validate(u: string, ver: i32): i32 {
    if (u.len() != 36) { return 100; }
    if (u[8] != 45 || u[13] != 45 || u[18] != 45 || u[23] != 45) { return 101; }
    if (u[14] != ver) { return 102; }
    var v: i32 = u[19];
    if (v != 56 && v != 57 && v != 97 && v != 98) { return 103; }
    return 42;
}
`
	cases := []struct {
		name string
		src  string
	}{
		{"v4", helpers + validate + `function main(): i32 { return validate(uuid_v4(), 52); }`}, // '4'
		{"v7", helpers + validate + `function main(): i32 { return validate(uuid_v7(), 55); }`}, // '7'
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emitAndRunIR(t, tc.src); got != 42 {
				t.Errorf("self-host IR uuid %q: structural check = %d, want 42 (valid UUID)", tc.name, got)
			}
		})
	}
}
