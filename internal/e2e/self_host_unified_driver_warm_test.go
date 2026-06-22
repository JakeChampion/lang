package e2e

import (
	"testing"
)

// TestSelfHostUnifiedDriverWarm builds the unified x86-family self-host driver
// and exercises one program through every mode. It serves two purposes:
//
//   - Regression guard: a single program is run through interp / vm / asm /
//     ir-x86 / check / elig and the value-producing modes must agree on its
//     result, so a backend that diverges (or a driver mode that breaks) fails
//     here rather than silently in one of the many probe tests that share this
//     driver.
//   - Cache warmer: building the driver populates the content-addressed disk
//     cache (FERN_SELFHOST_BUILD_CACHE). CI runs this test by name in the short
//     `build` job so the ~70s front-end compile happens once, off the critical
//     path; the sharded `test` jobs then restore the cache and serve every
//     unified-driver test from disk (~1s) instead of recompiling — which is
//     what kept the x86 self-host shards short enough to dodge hosted-runner
//     preemption. See examples/self_host/selfhost_x86_run.fern.
func TestSelfHostUnifiedDriverWarm(t *testing.T) {
	runner, bin := selfHostX86Driver(t)

	// add(2, 3*4) == 14 — a function call plus arithmetic precedence, which
	// each value-producing backend must evaluate identically.
	const prog = "function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(2, 3 * 4); }"
	for _, mode := range []string{"interp", "vm"} {
		if got := runUnifiedExit(t, runner, bin, mode, prog); got != 14 {
			t.Errorf("unified driver mode=%s of add(2,3*4) = %d, want 14", mode, got)
		}
	}
	// check: the program is well-typed → exit 0.
	if got := runUnifiedExit(t, runner, bin, "check", prog); got != 0 {
		t.Errorf("unified driver mode=check of a well-typed program = %d, want 0", got)
	}
	// elig: a plain i32 function-call program is fully IR-eligible → exit 1.
	if got := runUnifiedExit(t, runner, bin, "elig", prog); got != 1 {
		t.Errorf("unified driver mode=elig of an i32 program = %d, want 1", got)
	}
	// asm / ir-x86: both emit non-empty assembly (exit 0; output on stdout).
	for _, mode := range []string{"asm", "ir-x86"} {
		if got := runUnifiedExit(t, runner, bin, mode, prog); got != 0 {
			t.Errorf("unified driver mode=%s exited %d, want 0", mode, got)
		}
	}
}
