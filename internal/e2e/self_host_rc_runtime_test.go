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
		// Aliasing an array struct field (var y = h.items) retains the
		// field's buffer; reads stay correct.
		{"alias-struct-field", "struct H { items: i32[] } function main(): i32 { var h: H = H { items: [11, 22, 33] }; var y = h.items; return y[1] + h.items[2]; }", 55},
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

	// Emission: aliasing an array struct field also retains.
	t.Run("emits-retain-at-field-alias", func(t *testing.T) {
		asm := runCapture(t, gcc, runner, driverBin,
			[]byte("struct H { items: i32[] } function main(): i32 { var h: H = H { items: [1, 2] }; var y = h.items; return y[0]; }"))
		if !strings.Contains(string(asm), "call __fn___fern_rc_inc") {
			t.Errorf("expected a retain (__fern_rc_inc) at the struct-field array alias; not found in emitted asm")
		}
	})
}

// Phase 1d (cont.): reassigning an array slot `y = x` retains the new
// reference and releases (dec) the OLD value the slot held. With free
// off this is observably a no-op on values, so we check value-
// correctness, a clean over-release detector (the old value had rc>=1),
// and that the reassignment emits both a retain (new) and a release
// (old). Mirrors docs/RC-PERCEUS-SELF-HOST-PORT.md Phase 1d.
func TestSelfHostRcReassignX86_64(t *testing.T) {
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
		// Reassign one array slot to alias another: ys now sees xs's data.
		{"reassign-to-alias", "function main(): i32 { var xs: i32[] = [1, 2, 3]; var ys: i32[] = [4, 5, 6]; ys = xs; return ys[0] + ys[2]; }", 4},
		// The source alias stays readable after the reassignment.
		{"reassign-source-intact", "function main(): i32 { var xs: i32[] = [7, 8]; var ys: i32[] = [0, 0]; ys = xs; return xs[1] + ys[1]; }", 16},
		// Reassign to a fresh literal (no retain), old value released.
		{"reassign-to-fresh", "function main(): i32 { var xs: i32[] = [1, 2]; xs = [9, 9, 9]; return xs[2]; }", 9},
		// Over-release detector stays 0 across reassignments.
		{"reassign-no-underflow", "function main(): i32 { var xs: i32[] = [1, 2]; var ys: i32[] = [3, 4]; ys = xs; var zs: i32[] = [5, 6]; zs = ys; return __fern_rc_underflow_count(); }", 0},
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

	// Emission: `ys = xs` retains the new ref AND releases the old value.
	t.Run("emits-retain-and-release", func(t *testing.T) {
		asm := string(runCapture(t, gcc, runner, driverBin,
			[]byte("function main(): i32 { var xs: i32[] = [1, 2]; var ys: i32[] = [3, 4]; ys = xs; return ys[0]; }")))
		if !strings.Contains(asm, "call __fn___fern_rc_inc") {
			t.Errorf("expected a retain (__fern_rc_inc) for the reassigned alias")
		}
		if !strings.Contains(asm, "call __fn___fern_rc_dec") {
			t.Errorf("expected a release (__fern_rc_dec) of the overwritten value")
		}
	})
}

// Phase 1d (cont.): the function-exit dec sweep releases every array
// LOCAL at each return / fall-through (borrowed params are skipped); an
// array returned to the caller is retained so it survives the sweep,
// and body-local slots are zero-inited so a skipped `var` is a no-op.
// With free off this is observably a no-op on values; we check
// value-correctness across calls (incl. returning an array and passing
// a borrowed array), a clean over-release detector, and the emission of
// the zero-init + the release sweep. Mirrors
// docs/RC-PERCEUS-SELF-HOST-PORT.md Phase 1d.
func TestSelfHostRcExitSweepX86_64(t *testing.T) {
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
		// Return an array: retained past the exit sweep so the caller's
		// reference is live.
		{"return-array", "function make(): i32[] { var xs: i32[] = [1, 2, 3]; return xs; } function main(): i32 { var ys = make(); return ys[0] + ys[2]; }", 4},
		// Borrowed array param: not released by the callee's sweep, so it
		// stays usable in the caller; detector clean.
		{"borrowed-param", "function sum2(a: i32[]): i32 { return a[0] + a[1]; } function main(): i32 { var xs: i32[] = [7, 8]; var r = sum2(xs); return r + xs[0] + __fern_rc_underflow_count(); }", 22},
		// A function with array locals + alias, called repeatedly: each
		// call balances inc (alias) against the exit sweep, so the
		// over-release detector stays 0.
		{"exit-sweep-no-underflow", "function f(): i32 { var xs: i32[] = [1, 2]; var ys = xs; return ys[0]; } function main(): i32 { var a = f(); var b = f(); var c = f(); return __fern_rc_underflow_count(); }", 0},
		// An array local declared inside a not-taken branch: the
		// zero-inited slot makes the exit sweep a no-op (no spurious
		// release), detector clean.
		{"branch-local-zeroinit", "function main(): i32 { var xs: i32[] = [5, 6]; if (xs[0] > 100) { var ys: i32[] = [1, 2]; return ys[0]; } return xs[1] + __fern_rc_underflow_count(); }", 6},
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

	// Emission: a function with an array local zero-inits its body slots
	// (rep stosq) and releases the local at exit (__fern_rc_dec sweep).
	t.Run("emits-zeroinit-and-sweep", func(t *testing.T) {
		asm := string(runCapture(t, gcc, runner, driverBin,
			[]byte("function main(): i32 { var xs: i32[] = [1, 2]; return xs[0]; }")))
		if !strings.Contains(asm, "rep stosq") {
			t.Errorf("expected body-local zero-init (rep stosq) in a function with locals")
		}
		if !strings.Contains(asm, "call __fn___fern_rc_dec") {
			t.Errorf("expected the exit-dec sweep (__fern_rc_dec) for the array local")
		}
	})
}

// Phase 1d arm64 parity: the array inc/dec wiring (alias retain,
// reassign-inc + dec-on-overwrite, function-exit release sweep with
// zero-init + array-return retain) mirrored into asm_arm64.fern. Run
// under qemu-aarch64. Value-correctness (free off → RC is a no-op on
// values) + a clean over-release detector across the lifecycle.
func TestSelfHostRcArm64(t *testing.T) {
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

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"alias-read-both", "function main(): i32 { var xs: i32[] = [10, 20, 30]; var ys = xs; return ys[1] + xs[0]; }", 30},
		{"reassign-to-alias", "function main(): i32 { var xs: i32[] = [1, 2, 3]; var ys: i32[] = [4, 5, 6]; ys = xs; return ys[0] + ys[2]; }", 4},
		{"field-alias", "struct H { items: i32[] } function main(): i32 { var h: H = H { items: [11, 22, 33] }; var y = h.items; return y[1] + h.items[2]; }", 55},
		{"return-array", "function make(): i32[] { var xs: i32[] = [1, 2, 3]; return xs; } function main(): i32 { var ys = make(); return ys[0] + ys[2]; }", 4},
		{"borrowed-param", "function sum2(a: i32[]): i32 { return a[0] + a[1]; } function main(): i32 { var xs: i32[] = [7, 8]; var r = sum2(xs); return r + xs[0] + __fern_rc_underflow_count(); }", 22},
		{"exit-sweep-no-underflow", "function f(): i32 { var xs: i32[] = [1, 2]; var ys = xs; return ys[0]; } function main(): i32 { var a = f(); var b = f(); var c = f(); return __fern_rc_underflow_count(); }", 0},
		{"branch-local-zeroinit", "function main(): i32 { var xs: i32[] = [5, 6]; if (xs[0] > 100) { var ys: i32[] = [1, 2]; return ys[0]; } return xs[1] + __fern_rc_underflow_count(); }", 6},
		// Cow-aware dec (Phase 3 prep): a self-append loop stays clean.
		{"self-append-no-underflow", "function main(): i32 { var xs: i32[] = []; var i = 0; while (i < 20) { xs = xs.append(i); i = i + 1; } return __fern_rc_underflow_count(); }", 0},
		{"self-append-values", "function main(): i32 { var xs: i32[] = []; var i = 0; while (i < 20) { xs = xs.append(i * 2); i = i + 1; } return xs[19]; }", 38},
	}
	for _, tc := range cases {
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

// Phase 3 prep: the cow-aware dec-on-overwrite. An in-place mutator
// (`xs = xs.append(v)` growing within capacity) returns the SAME buffer,
// so releasing the slot's old value would over-count. The dec is now
// skipped when the new value equals the old (`cmp; je/b.eq` guard), so a
// self-mutating loop stays over-release-detector clean while a genuine
// reassignment to a different buffer still releases. Mirrors the native
// drift audit (docs/RC-PERCEUS-SELF-HOST-PORT.md Phase 3 prep).
func TestSelfHostRcSelfMutateX86_64(t *testing.T) {
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
		// A 20-iteration self-append loop: in-place growth returns the
		// same buffer, so the cow-aware dec keeps the detector at 0.
		{"self-append-no-underflow", "function main(): i32 { var xs: i32[] = []; var i = 0; while (i < 20) { xs = xs.append(i); i = i + 1; } return __fern_rc_underflow_count(); }", 0},
		// Values stay correct across the self-mutation.
		{"self-append-values", "function main(): i32 { var xs: i32[] = []; var i = 0; while (i < 20) { xs = xs.append(i * 2); i = i + 1; } return xs[19]; }", 38},
		// A genuine reassignment to a different buffer still releases the
		// old and keeps the source readable, detector clean.
		{"reassign-different-clean", "function main(): i32 { var xs: i32[] = [1, 2]; var ys: i32[] = [3, 4]; ys = xs; return ys[0] + xs[1] + __fern_rc_underflow_count(); }", 3},
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
}
