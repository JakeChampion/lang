package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Phase 0c: the self-hosted backends' RC runtime helpers
// (__fern_rc_inc / __fern_rc_dec / __fern_rc_is_unique /
// __fern_rc_underflow_count), ported from the native backends. The rc
// word is a 32-bit count at [data-8]. These tests hand-build an
// rc-headered object via __alloc + __store_i32 and exercise the helpers
// directly — they are not yet wired into real allocations (that's the
// layout-migration slice), so __alloc + the raw pokes are the way to
// reach them. The array literal forces the heap runtime (which carries
// these helpers) to be emitted. Guard chain (null / SSO inline-tag /
// low-address / static-sentinel) and the over-release detector are
// covered. Mirrors docs/RC-PERCEUS-SELF-HOST-PORT.md Phase 0c.
var rcRuntimeCases = []struct {
	name string
	src  string
	exit int
}{
	// rc=5; inc, inc, dec -> 6.
	{"rc-inc-dec", "function main(): i32 { var f: i32[] = [0]; var base: usize = __alloc(16); __store_i32(base, 5); __fern_rc_inc(base + 8); __fern_rc_inc(base + 8); __fern_rc_dec(base + 8); return __load_i32(base); }", 6},
	// rc==1 -> unique.
	{"rc-is-unique-true", "function main(): i32 { var f: i32[] = [0]; var base: usize = __alloc(16); __store_i32(base, 1); if (__fern_rc_is_unique(base + 8) == 1) { return 1; } return 0; }", 1},
	// rc==2 -> not unique.
	{"rc-is-unique-false", "function main(): i32 { var f: i32[] = [0]; var base: usize = __alloc(16); __store_i32(base, 2); if (__fern_rc_is_unique(base + 8) == 1) { return 1; } return 0; }", 0},
	// rc=1; dec (->0, ok), dec (0 is not >0 -> over-release) -> detector == 1.
	{"rc-underflow-detected", "function main(): i32 { var f: i32[] = [0]; var base: usize = __alloc(16); __store_i32(base, 1); __fern_rc_dec(base + 8); __fern_rc_dec(base + 8); return __fern_rc_underflow_count(); }", 1},
	// rc=3; two decs stay > 0 -> detector == 0 (clean).
	{"rc-underflow-clean", "function main(): i32 { var f: i32[] = [0]; var base: usize = __alloc(16); __store_i32(base, 3); __fern_rc_dec(base + 8); __fern_rc_dec(base + 8); return __fern_rc_underflow_count(); }", 0},
	// null is a no-op (guard) — program returns normally.
	{"rc-inc-null-safe", "function main(): i32 { var f: i32[] = [0]; __fern_rc_inc(0); __fern_rc_dec(0); return 7; }", 7},
}

// TestSelfHostRcRuntimeX86_64 — RC runtime helpers via the self-hosted
// x86-64 backend.
func TestSelfHostRcRuntimeX86_64(t *testing.T) {
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

	for _, tc := range rcRuntimeCases {
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

// TestSelfHostRcRuntimeArm64 — CI-gated arm64 counterpart.
func TestSelfHostRcRuntimeArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"asmcore.fern", "lexer.fern", "parser.fern", "asm_arm64.fern", "asm_arm64_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_run.fern", "driver")

	for _, tc := range rcRuntimeCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// Phase 1d: the self-host x86-64 backend retains (inc) an rc-tracked
// array buffer when a `var y = x` binding creates a second reference to
// it. With free off (safe-leak) the inc has no observable effect on
// values, so these tests check (1) aliasing programs still compute the
// right result, (2) the over-release detector stays 0 (inc-only never
// drives an rc <= 0), and (3) the emitter actually emits the retain at
// the alias site. Mirrors docs/RC-PERCEUS-SELF-HOST-PORT.md Phase 1d.
func TestSelfHostRcAliasIncX86_64(t *testing.T) {
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

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// Aliasing an array stays value-correct: ys and xs see the same
		// buffer; the retain doesn't disturb the contents.
		{"alias-read-both", "function main(): i32 { var xs: i32[] = [10, 20, 30]; var ys = xs; return ys[1] + xs[0]; }", 30},
		// Multiple aliases of the same buffer all read correctly.
		{"alias-chain", "function main(): i32 { var xs: i32[] = [4, 5, 6]; var ys = xs; var zs = ys; return zs[0] + ys[1] + xs[2]; }", 15},
		// An aliasing program leaves the over-release detector at 0
		// (inc-only: rc only grows, never crosses 0).
		{"alias-no-underflow", "function main(): i32 { var xs: i32[] = [5, 6, 7]; var ys = xs; var zs = ys; return __fern_rc_underflow_count(); }", 0},
	}
	for _, tc := range cases {
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

	// Emission: the `var ys = xs` alias must emit a retain on the array
	// buffer. A fresh literal binding (`var xs = [...]`) must NOT.
	t.Run("emits-retain-at-alias", func(t *testing.T) {
		asm := runCapture(t, gcc, runner, driverBin,
			[]byte("function main(): i32 { var xs: i32[] = [1, 2]; var ys = xs; return ys[0]; }"))
		if !strings.Contains(string(asm), "call __fn___fern_rc_inc") {
			t.Errorf("expected a retain (__fern_rc_inc) at the array alias; not found in emitted asm")
		}
	})
}
