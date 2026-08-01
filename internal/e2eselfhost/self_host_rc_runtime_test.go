package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Phase 0c: the self-hosted backends' RC runtime helpers
// (__fern_rc_inc / __fern_rc_dec / __fern_rc_is_unique /
// __rc_underflow), ported from the native backends. The rc
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
	{"rc-underflow-detected", "function main(): i32 { var f: i32[] = [0]; var base: usize = __alloc(16); __store_i32(base, 1); __fern_rc_dec(base + 8); __fern_rc_dec(base + 8); return __rc_underflow(); }", 1},
	// rc=3; two decs stay > 0 -> detector == 0 (clean).
	{"rc-underflow-clean", "function main(): i32 { var f: i32[] = [0]; var base: usize = __alloc(16); __store_i32(base, 3); __fern_rc_dec(base + 8); __fern_rc_dec(base + 8); return __rc_underflow(); }", 0},
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
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range rcRuntimeCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
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
		{"alias-no-underflow", "function main(): i32 { var xs: i32[] = [5, 6, 7]; var ys = xs; var zs = ys; return __rc_underflow(); }", 0},
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
		{"reassign-no-underflow", "function main(): i32 { var xs: i32[] = [1, 2]; var ys: i32[] = [3, 4]; ys = xs; var zs: i32[] = [5, 6]; zs = ys; return __rc_underflow(); }", 0},
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
		if !strings.Contains(asm, "call __fn___fern_arr_dec") {
			t.Errorf("expected a release (__fern_arr_dec) of the overwritten value")
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
		{"borrowed-param", "function sum2(a: i32[]): i32 { return a[0] + a[1]; } function main(): i32 { var xs: i32[] = [7, 8]; var r = sum2(xs); return r + xs[0] + __rc_underflow(); }", 22},
		// A function with array locals + alias, called repeatedly: each
		// call balances inc (alias) against the exit sweep, so the
		// over-release detector stays 0.
		{"exit-sweep-no-underflow", "function f(): i32 { var xs: i32[] = [1, 2]; var ys = xs; return ys[0]; } function main(): i32 { var a = f(); var b = f(); var c = f(); return __rc_underflow(); }", 0},
		// An array local declared inside a not-taken branch: the
		// zero-inited slot makes the exit sweep a no-op (no spurious
		// release), detector clean.
		{"branch-local-zeroinit", "function main(): i32 { var xs: i32[] = [5, 6]; if (xs[0] > 100) { var ys: i32[] = [1, 2]; return ys[0]; } return xs[1] + __rc_underflow(); }", 6},
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
		if !strings.Contains(asm, "call __fn___fern_arr_dec") {
			t.Errorf("expected the exit-dec sweep (__fern_arr_dec) for the array local")
		}
	})
}

// Phase 4 (Perceus move-on-return): `return xs` where xs is a bare
// owned array LOCAL (slot index >= n_params) hands the buffer to the
// caller directly — the return-retain inc and the exit sweep's dec of
// that slot are a balanced pair, so both are elided. The buffer reaches
// the caller at its current rc with identical net effect. These cases
// verify both the runtime correctness (clean over-release detector,
// correct values) and the emission (no retain inc in the elided path,
// but a retain inc IS still emitted when the optimization does not
// apply, e.g. returning a non-ident array expression).
func TestSelfHostRcMoveOnReturnX86_64(t *testing.T) {
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
		// Bare owned local returned: moved to caller, values intact, detector clean.
		{"move-bare-local", "function make(): i32[] { var xs: i32[] = [10, 20, 30]; return xs; } function main(): i32 { var ys = make(); return ys[0] + ys[2] + __rc_underflow(); }", 40},
		// Moved through a chain of callers (each return moves), detector clean.
		{"move-chained", "function mk(): i32[] { var xs: i32[] = [1, 2, 3]; return xs; } function relay(): i32[] { var a = mk(); return a; } function main(): i32 { var b = relay(); return b[0] + b[2] + __rc_underflow(); }", 4},
		// Move co-exists with a sibling array local that IS swept (only the
		// returned slot is excluded): sibling release keeps detector clean.
		{"move-with-sibling-sweep", "function make(): i32[] { var keep: i32[] = [9, 9]; var xs: i32[] = [5, 6, 7]; return xs; } function main(): i32 { var ys = make(); return ys[0] + ys[2] + __rc_underflow(); }", 12},
		// Returning a BORROWED param array is NOT a move (idx < n_params):
		// the caller still owns it, so it stays usable after the call.
		{"return-param-not-moved", "function pick(a: i32[]): i32[] { return a; } function main(): i32 { var xs: i32[] = [3, 4]; var ys = pick(xs); return ys[0] + xs[1] + __rc_underflow(); }", 7},
		// Churn: a builder whose result is moved out on every call must
		// still allow reclamation (alloc >> heap completes via the freelist).
		{"move-churn", "function build(n: i32): i32[] { var xs: i32[] = []; var i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs; } function main(): i32 { var k = 0; var s = 0; while (k < 200000) { var r = build(64); s = r[63]; k = k + 1; } return (s % 7) + __rc_underflow(); }", 0},
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

	// Emission: returning a bare owned array local elides the retain inc
	// (no `call __fn___fern_rc_inc` for that path), whereas returning a
	// non-ident array expression (a field access) still emits it.
	t.Run("emits-no-inc-on-move", func(t *testing.T) {
		asm := string(runCapture(t, gcc, runner, driverBin,
			[]byte("function make(): i32[] { var xs: i32[] = [1, 2, 3]; return xs; } function main(): i32 { var ys = make(); return ys[0]; }")))
		if strings.Contains(asm, "call __fn___fern_rc_inc") {
			t.Errorf("move-on-return should elide the retain inc, but found call __fn___fern_rc_inc")
		}
	})
	t.Run("emits-inc-when-not-moved", func(t *testing.T) {
		asm := string(runCapture(t, gcc, runner, driverBin,
			[]byte("struct H { items: i32[] } function get(h: H): i32[] { return h.items; } function main(): i32 { var hh: H = H { items: [4, 5, 6] }; var ys = get(hh); return ys[0]; }")))
		if !strings.Contains(asm, "call __fn___fern_rc_inc") {
			t.Errorf("returning a non-local array expression should still emit the retain inc")
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
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"alias-read-both", "function main(): i32 { var xs: i32[] = [10, 20, 30]; var ys = xs; return ys[1] + xs[0]; }", 30},
		{"reassign-to-alias", "function main(): i32 { var xs: i32[] = [1, 2, 3]; var ys: i32[] = [4, 5, 6]; ys = xs; return ys[0] + ys[2]; }", 4},
		{"field-alias", "struct H { items: i32[] } function main(): i32 { var h: H = H { items: [11, 22, 33] }; var y = h.items; return y[1] + h.items[2]; }", 55},
		{"return-array", "function make(): i32[] { var xs: i32[] = [1, 2, 3]; return xs; } function main(): i32 { var ys = make(); return ys[0] + ys[2]; }", 4},
		{"borrowed-param", "function sum2(a: i32[]): i32 { return a[0] + a[1]; } function main(): i32 { var xs: i32[] = [7, 8]; var r = sum2(xs); return r + xs[0] + __rc_underflow(); }", 22},
		{"exit-sweep-no-underflow", "function f(): i32 { var xs: i32[] = [1, 2]; var ys = xs; return ys[0]; } function main(): i32 { var a = f(); var b = f(); var c = f(); return __rc_underflow(); }", 0},
		{"branch-local-zeroinit", "function main(): i32 { var xs: i32[] = [5, 6]; if (xs[0] > 100) { var ys: i32[] = [1, 2]; return ys[0]; } return xs[1] + __rc_underflow(); }", 6},
		// Cow-aware dec (Phase 3 prep): a self-append loop stays clean.
		{"self-append-no-underflow", "function main(): i32 { var xs: i32[] = []; var i = 0; while (i < 20) { xs = xs.append(i); i = i + 1; } return __rc_underflow(); }", 0},
		{"self-append-values", "function main(): i32 { var xs: i32[] = []; var i = 0; while (i < 20) { xs = xs.append(i * 2); i = i + 1; } return xs[19]; }", 38},
		// Construction store: struct field + array-of-arrays capture.
		{"struct-holds-array", "struct H { items: i32[] } function mk(): H { var xs: i32[] = [7, 8]; return H { items: xs }; } function main(): i32 { var h = mk(); return h.items[0] + h.items[1] + __rc_underflow(); }", 15},
		{"array-of-arrays", "function main(): i32 { var a: i32[] = [1, 2]; var b: i32[] = [3, 4]; var both: i32[][] = [a, b]; return both[0][1] + both[1][0] + __rc_underflow(); }", 5},
		{"struct-update-copy", "struct H { items: i32[], n: i32 } function main(): i32 { var xs: i32[] = [1, 2]; var h: H = H { items: xs, n: 0 }; var h2: H = H { ...h, n: 5 }; return h2.items[1] + h2.n + __rc_underflow(); }", 7},
		// Phase 3 (arm64 free): reclamation churn (alloc >> heap completes) + enum payload retain.
		{"reclaim-churn", "function work(n: i32): i32 { var xs: i32[] = []; var i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs[n - 1]; } function main(): i32 { var k = 0; var s = 0; while (k < 200000) { s = work(200); k = k + 1; } return (s % 7) + __rc_underflow(); }", 3},
		{"enum-holds-array", "enum Box { Arr(i32[]), Empty } function mk(): Box { var xs: i32[] = [3, 4, 5]; return Arr(xs); } function main(): i32 { var b = mk(); match (b) { Arr(a) => { return a[1] + a[2] + __rc_underflow(); }, Empty => { return 0; } } }", 9},
		// Phase 4 (move-on-return): bare owned local moved to caller; sibling
		// local still swept; borrowed-param return is not a move.
		{"move-bare-local", "function make(): i32[] { var xs: i32[] = [10, 20, 30]; return xs; } function main(): i32 { var ys = make(); return ys[0] + ys[2] + __rc_underflow(); }", 40},
		{"move-with-sibling-sweep", "function make(): i32[] { var keep: i32[] = [9, 9]; var xs: i32[] = [5, 6, 7]; return xs; } function main(): i32 { var ys = make(); return ys[0] + ys[2] + __rc_underflow(); }", 12},
		{"return-param-not-moved", "function pick(a: i32[]): i32[] { return a; } function main(): i32 { var xs: i32[] = [3, 4]; var ys = pick(xs); return ys[0] + xs[1] + __rc_underflow(); }", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
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
		{"self-append-no-underflow", "function main(): i32 { var xs: i32[] = []; var i = 0; while (i < 20) { xs = xs.append(i); i = i + 1; } return __rc_underflow(); }", 0},
		// Values stay correct across the self-mutation.
		{"self-append-values", "function main(): i32 { var xs: i32[] = []; var i = 0; while (i < 20) { xs = xs.append(i * 2); i = i + 1; } return xs[19]; }", 38},
		// A genuine reassignment to a different buffer still releases the
		// old and keeps the source readable, detector clean.
		{"reassign-different-clean", "function main(): i32 { var xs: i32[] = [1, 2]; var ys: i32[] = [3, 4]; ys = xs; return ys[0] + xs[1] + __rc_underflow(); }", 3},
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

// Phase 1d (construction store): a struct field initialised from an
// rc-tracked array alias retains the buffer, so the struct's reference
// is counted. This is the free-readiness prerequisite — without it, a
// struct outliving the source local would dangle once free is on.
// inc-only here (struct drop isn't wired), so detector-clean + safe.
func TestSelfHostRcConstructX86_64(t *testing.T) {
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
		// Struct captures an array alias; both readable, detector clean.
		{"struct-holds-array", "struct H { items: i32[] } function main(): i32 { var xs: i32[] = [1, 2, 3]; var h: H = H { items: xs }; return h.items[1] + xs[0] + __rc_underflow(); }", 3},
		// The capture survives the source local going out of scope (the
		// case that would UAF once free is on without the construction inc).
		{"struct-outlives-source", "struct H { items: i32[] } function mk(): H { var xs: i32[] = [7, 8]; return H { items: xs }; } function main(): i32 { var h = mk(); return h.items[0] + h.items[1] + __rc_underflow(); }", 15},
		// A struct field from a fresh literal is owned — not re-incremented.
		{"struct-fresh-literal", "struct H { items: i32[] } function main(): i32 { var h: H = H { items: [4, 5, 6] }; return h.items[2] + __rc_underflow(); }", 6},
		// Struct-update copies the base's array field (retained for soundness).
		{"struct-update-copy", "struct H { items: i32[], n: i32 } function main(): i32 { var xs: i32[] = [1, 2]; var h: H = H { items: xs, n: 0 }; var h2: H = H { ...h, n: 5 }; return h2.items[1] + h2.n + __rc_underflow(); }", 7},
		{"struct-update-override", "struct H { items: i32[], n: i32 } function main(): i32 { var xs: i32[] = [9, 8]; var h: H = H { items: [0], n: 1 }; var h2: H = H { ...h, items: xs }; return h2.items[0] + __rc_underflow(); }", 9},
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
	// Emission: a struct field from an array alias retains.
	t.Run("emits-retain-at-field-init", func(t *testing.T) {
		asm := string(runCapture(t, gcc, runner, driverBin,
			[]byte("struct H { items: i32[] } function main(): i32 { var xs: i32[] = [1, 2]; var h: H = H { items: xs }; return h.items[0]; }")))
		if !strings.Contains(asm, "call __fn___fern_rc_inc") {
			t.Errorf("expected a retain (__fern_rc_inc) at the struct field init")
		}
	})
}

// Phase 1d (array/tuple construction store): an array/tuple element
// initialised from an rc-tracked array alias retains the buffer (the
// container owns a new reference) — the array-literal and tuple-literal
// arms of the free-readiness gate. inc-only / detector-clean / safe.
func TestSelfHostRcConstructContainersX86_64(t *testing.T) {
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
		// Array of arrays: the inner array aliases are retained.
		{"array-of-arrays", "function main(): i32 { var a: i32[] = [1, 2]; var b: i32[] = [3, 4]; var both: i32[][] = [a, b]; return both[0][1] + both[1][0] + __rc_underflow(); }", 5},
		// Tuple holding an array: the array element is retained.
		{"tuple-of-array", "function main(): i32 { var xs: i32[] = [7, 8]; var t = (xs, 9); return t.0[1] + t.1 + __rc_underflow(); }", 17},
		// Returning a container that captured a local array (would UAF
		// once free is on without the construction inc) stays correct.
		{"return-arr-of-arrs", "function mk(): i32[][] { var a: i32[] = [5, 6]; return [a, a]; } function main(): i32 { var both = mk(); return both[0][0] + both[1][1] + __rc_underflow(); }", 11},
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

// Phase 1d (closure capture): a lambda capturing an rc-tracked array
// retains the buffer (the closure box owns the reference). Uses the
// block-form lambda (`function (): T { ... }`); the arrow form
// `() => e` capturing a local is a separate pre-existing self-host
// limitation. inc-only / detector-clean / safe (free off).
func TestSelfHostRcClosureX86_64(t *testing.T) {
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
		// Local closure capturing an array local.
		{"closure-captures-array", "function main(): i32 { var xs: i32[] = [3, 4, 5]; var f = function (): i32 { return xs[1] + xs[2]; }; return f() + __rc_underflow(); }", 9},
		// Closure escaping its defining function, capturing an array.
		{"closure-escapes-with-array", "function mk(xs: i32[]): () => i32 { return function (): i32 { return xs[0] + xs[1]; }; } function main(): i32 { var a: i32[] = [3, 4]; var f = mk(a); return f() + __rc_underflow(); }", 7},
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

// Phase 3: free is ON for arrays (x86-64) -- buffers reclaimed via a
// size-class freelist + __fern_arr_dec at rc==1. Reclamation proof + the
// enum-payload retain that closed the JSON nested-structure gap.
func TestSelfHostRcFreeReclaimX86_64(t *testing.T) {
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
		// Total allocation far exceeds the 1 GiB bump heap; completes
		// (exit 0) only because freed buffers are reused. (n<256 avoids a
		// pre-existing, unrelated .append grow bug.)
		{"reclaim-churn", "function work(n: i32): i32 { var xs: i32[] = []; var i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs[n - 1]; } function main(): i32 { var k = 0; var s = 0; while (k < 500000) { s = work(200); k = k + 1; } return (s % 7) + __rc_underflow(); }", 3},
		// Borrowed-param builder: callee must not free the caller's buffer.
		{"borrowed-param-builder", "function add(toks: i32[], t: i32): i32[] { return toks.append(t); } function main(): i32 { var ts: i32[] = []; var i = 0; while (i < 200) { ts = add(ts, i); i = i + 1; } return ts[199] + __rc_underflow(); }", 199},
		// Enum payload holding an array: the variant retains it, so the
		// source local going out of scope does not free it (would UAF
		// once free is on -- the JSON nested-structure gap).
		{"enum-holds-array", "enum Box { Arr(i32[]), Empty } function mk(): Box { var xs: i32[] = [3, 4, 5]; return Arr(xs); } function main(): i32 { var b = mk(); match (b) { Arr(a) => { return a[1] + a[2] + __rc_underflow(); }, Empty => { return 0; } } }", 9},
		// Loop-local array rebind: `var r = build(n)` re-bound each iteration
		// is released per-iteration (StmtVar cow-guarded dec-on-overwrite),
		// not leaked until function exit. 100k rebinds stay value-correct and
		// over-release-detector clean.
		{"loop-local-rebind", "function build(n: i32): i32[] { var xs: i32[] = []; var i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs; } function main(): i32 { var s = 0; var k = 0; while (k < 100000) { var r: i32[] = build(32); s = s + r[31]; k = k + 1; } return (s % 5) + __rc_underflow(); }", 0},
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

// TestSelfHostRcStructArrayFieldDropX86_64 covers the Perceus deep-drop (one
// level) of array-of-struct / array-of-enum FIELDS: when a reclaimable struct
// is dropped, emit_struct_field_drops releases its struct/enum-array field
// BUFFERS via a shallow __fern_arr_dec (the element boxes still leak — the
// safe-leak invariant for nested payloads). This BALANCES the alias-inc the
// struct-lit / bind / return / assign paths add when such a field aliases a
// local, so the over-release detector must stay 0 across all construction
// shapes — fresh literal (sole owner), aliased ident/param (inc'd), and a fresh
// call value (sole owner). A double-free here would trip the detector or crash.
func TestSelfHostRcStructArrayFieldDropX86_64(t *testing.T) {
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
		// A reclaimable struct with a struct-array field built from a FRESH
		// literal (sole owner): dropped 2000x, the field buffer is reclaimed
		// each time, detector clean.
		{"struct-arr-field-fresh-no-underflow", "struct E { v: i32 } struct H { es: E[] } function step(n: i32): i32 { var h = H { es: [E { v: n }, E { v: n + 1 }] }; return h.es[0].v; } function main(): i32 { var s = 0; var i = 0; while (i < 2000) { s = s + step(i); i = i + 1; } return s - s + __rc_underflow(); }", 0},
		// Struct-array field ALIASED from a borrowed param ident: construction
		// incs the buffer, the struct's reclamation field-drop decs it, the
		// borrowed param is not swept — balanced, detector clean across 2000 calls.
		// NOTE: the helper must not be named `use` — that became a reserved
		// keyword (the `use x <- call();` monadic bind, #4335/#4450) after this
		// case landed, and the self-host parser then silently miscompiled the
		// program into an infinite loop (the `i = i + 1` increment lowered to
		// StmtUnknown), hanging the CI shard at the 18m go-test timeout. The
		// silent-miscompile-on-parse-error footgun is tracked in #4471.
		{"struct-arr-field-alias-no-underflow", "struct E { v: i32 } struct H { es: E[] } function wrapH(src: E[]): i32 { var h = H { es: src }; return h.es[0].v; } function main(): i32 { var shared: E[] = [E { v: 3 }, E { v: 4 }]; var s = 0; var i = 0; while (i < 2000) { s = s + wrapH(shared); i = i + 1; } return s - s + __rc_underflow(); }", 0},
		// Struct-array field from a fresh CALL value (sole owner, no inc): the
		// field-drop frees it; a non-fresh callee would over-free here.
		{"struct-arr-field-callvalue-no-underflow", "struct E { v: i32 } struct H { es: E[] } function mk(n: i32): E[] { return [E { v: n }, E { v: n * 2 }]; } function step(n: i32): i32 { var h = H { es: mk(n) }; return h.es[1].v; } function main(): i32 { var s = 0; var i = 0; while (i < 2000) { s = s + step(i); i = i + 1; } return s - s + __rc_underflow(); }", 0},
		// Array-of-ENUM field: the buffer is reclaimed the same shallow way.
		{"enum-arr-field-no-underflow", "enum K { A(i32), B } struct G { ks: K[] } function step(n: i32): i32 { var g = G { ks: [A(n), B] }; return match (g.ks[0]) { A(x) => x, B => 0 }; } function main(): i32 { var s = 0; var i = 0; while (i < 2000) { s = s + step(i); i = i + 1; } return s - s + __rc_underflow(); }", 0},
		// Value-correctness: the reclamation does not disturb the field reads
		// before the drop.
		{"struct-arr-field-value", "struct E { v: i32 } struct H { es: E[] } function main(): i32 { var h = H { es: [E { v: 5 }, E { v: 9 }] }; return h.es[0].v * 10 + h.es[1].v; }", 59},
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

	// Emission: a reclaimable struct whose ONLY rc field is a struct-array
	// releases that field's buffer (__fern_arr_dec) at the struct's reclamation,
	// AND deep-drops the element boxes — the walk is gated on __fern_rc_is_unique
	// (free the elements only when this drop frees the buffer, i.e. the sole
	// owner), then rc_dec's each element before the buffer dec.
	t.Run("emits-struct-array-field-drop", func(t *testing.T) {
		asm := string(runCapture(t, gcc, runner, driverBin,
			[]byte("struct E { v: i32 } struct H { es: E[] } function main(): i32 { var h = H { es: [E { v: 1 }] }; return h.es[0].v; }")))
		if !strings.Contains(asm, "call __fn___fern_arr_dec") {
			t.Errorf("expected a struct-array field buffer drop (__fern_arr_dec) at struct reclamation; not found")
		}
		if !strings.Contains(asm, "call __fn___fern_rc_is_unique") {
			t.Errorf("expected the element-walk sole-owner gate (__fern_rc_is_unique) at the struct-array field drop; not found")
		}
	})
}
