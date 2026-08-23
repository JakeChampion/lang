package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// auditPlatformCases isolate environment / clock / randomness / sleep
// built-ins and run them through the SELF-HOSTED compiler, asserting the
// exit code. Self-host arm of the §B audit (docs/FEATURE-AUDIT.md); the
// native arm is the `audit_env_time_random` fixture (env / now_unix_ms /
// random across all four native backends).
//
// monotonic_ns + sleep_ms are included here deliberately: the self-hosted
// compiler implements them (emits __fern_monotonic_ns / __fern_sleep_ms)
// even though the native x86-64/arm64 backends do not (#2843).
var auditPlatformCases = []struct {
	name string
	src  string
	exit int
}{
	{"env-unset-none", `function main(): i32 { match (env("FERN_AUDIT_UNSET_ZZZ")) { Some(v) => { return 1; }, None => { return 9; } } }`, 9},
	{"now-unix-ms-epoch", `function main(): i32 { var t: i64 = now_unix_ms(); if (t > 1600000000000) { return 7; } return 1; }`, 7},
	{"monotonic-ns-nondecreasing", `function main(): i32 { var a: i64 = monotonic_ns(); var b: i64 = monotonic_ns(); if (b >= a) { return 7; } return 1; }`, 7},
	{"sleep-ms", `function main(): i32 { sleep_ms(1); return 5; }`, 5},
	{"random-bytes-len", `function main(): i32 { var b: u8[] = random_bytes(16); return b.len(); }`, 16},
	{"random-i32-usable", `function main(): i32 { var x: i32 = random_i32(); if (x == x) { return 7; } return 1; }`, 7},
}

// TestSelfHostAuditPlatformX86_64 runs each platform builtin through the
// self-hosted x86-64 driver and asserts the exit code.
func TestSelfHostAuditPlatformX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range auditPlatformCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
