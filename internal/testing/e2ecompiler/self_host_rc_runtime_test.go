package e2ecompiler

import (
	"strings"
	"testing"
)

// The rc runtime helpers on a hand-built object, reached through the raw floor
// (`__alloc`, `__store_i32`, `__fern_rc_inc`, `__fern_rc_dec`) with `usize`
// addresses. The rc word is a 32-bit count at [data-8] and the length word at
// [data-16]. `__fern_rc_dec` is a release: at rc 1 it frees the object into the
// size-class freelist and leaves rc 0, so a second dec is the over-release the
// detector counts. The array literal forces the heap runtime to be emitted.
var rcRuntimeCases = []struct {
	name string
	src  string
	exit int
}{
	// rc=5; inc, inc, dec -> 6.
	{"rc-inc-dec", "function main(): i32 { let f: i32[] = [0]; let base: usize = __alloc(24); __store_i32(base + 8, 5); __fern_rc_inc(base + 16); __fern_rc_inc(base + 16); __fern_rc_dec(base + 16); return __load_i32(base + 8); }", 6},
	// rc=1, length 0; dec frees (rc -> 0), dec again is an over-release -> detector == 1.
	{"rc-underflow-detected", "function main(): i32 { let f: i32[] = [0]; let base: usize = __alloc(24); __store_i32(base + 8, 1); __fern_rc_dec(base + 16); __fern_rc_dec(base + 16); return __rc_underflow_count(); }", 1},
	// rc=3; two decs stay > 0 -> detector == 0 (clean).
	{"rc-underflow-clean", "function main(): i32 { let f: i32[] = [0]; let base: usize = __alloc(24); __store_i32(base + 8, 3); __fern_rc_dec(base + 16); __fern_rc_dec(base + 16); return __rc_underflow_count(); }", 0},
	// null is a no-op (guard) — program returns normally.
	{"rc-inc-null-safe", "function main(): i32 { let f: i32[] = [0]; __fern_rc_inc(0); __fern_rc_dec(0); return 7; }", 7},
}

// TestSelfHostRcRuntimeX86_64 — RC runtime helpers via the self-hosted
// x86-64 backend.
func TestSelfHostRcRuntimeX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range rcRuntimeCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostRcRuntimeArm64 — CI-gated arm64 counterpart.
func TestSelfHostRcRuntimeArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range rcRuntimeCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// An alias that outlives an update of its source; both answer 1 + 5 = 6 when
// the alias holds its own reference, and the underflow count is added in.
const (
	rcAliasUpdateSrc = "@noinline\nfunction mk(n: i32): i32[] { return [n, n + 1]; }\n" +
		"function main(): i32 { let xs: i32[] = mk(1); let ys = xs; xs = xs.with(0, 5); return ys[0] + xs[0] + __rc_underflow_count(); }"
	rcReassignUpdateSrc = "@noinline\nfunction mk(n: i32): i32[] { return [n, n + 1]; }\n" +
		"function main(): i32 { let xs: i32[] = mk(1); let ys: i32[] = mk(3); ys = xs; xs = xs.with(0, 5); return ys[0] + xs[0] + __rc_underflow_count(); }"
)

// A `let y = x` binding of an rc-tracked array: aliasing programs compute the
// right result, the over-release detector stays 0, and the retain is emitted
// where the alias really is a second owner and elided where it is a move.
func TestSelfHostRcAliasIncX86_64(t *testing.T) {
	boxedProbes(t)
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// Aliasing an array stays value-correct: ys and xs see the same
		// buffer; the retain doesn't disturb the contents.
		{"alias-read-both", "function main(): i32 { let xs: i32[] = [10, 20, 30]; let ys = xs; return ys[1] + xs[0]; }", 30},
		// Multiple aliases of the same buffer all read correctly.
		{"alias-chain", "function main(): i32 { let xs: i32[] = [4, 5, 6]; let ys = xs; let zs = ys; return zs[0] + ys[1] + xs[2]; }", 15},
		// An aliasing program leaves the over-release detector at 0
		// (inc-only: rc only grows, never crosses 0).
		{"alias-no-underflow", "function main(): i32 { let xs: i32[] = [5, 6, 7]; let ys = xs; let zs = ys; return __rc_underflow_count(); }", 0},
		// Aliasing an array struct field (let y = h.items) retains the
		// field's buffer; reads stay correct.
		{"alias-struct-field", "struct H { items: i32[] } function main(): i32 { let h: H = H { items: [11, 22, 33] }; let y = h.items; return y[1] + h.items[2]; }", 55},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}

	// Emission: a `let ys = xs` alias that must outlive an update of xs
	// retains the heap buffer, so the `.with` copies instead of writing
	// through ys. An alias only ever read shares xs's one reference and
	// needs no retain.
	t.Run("emits-retain-at-alias", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux", rcAliasUpdateSrc)
		if rcIncSites(asm) == 0 {
			t.Errorf("expected a retain (__fern_rc_inc) at the live-source array alias; not found in emitted asm")
		}
		if code, _ := cli.runX86(t, asm); code != 6 {
			t.Errorf("exited %d, want 6 (11 = the retain did nothing: .with wrote through ys, and the sweep over-released)", code)
		}
		// A retain emitted twice still answers 6; only the census sees the leak.
		stderr, code := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), "alias", cli.emit(t, "x86-64-linux", rcAliasUpdateSrc, "FERN_LEAKCHECK=1")))
		if code != 6 {
			t.Errorf("leakcheck build exited %d, want 6", code)
		}
		allocs, frees, live := parseLeakcheck(t, "emits-retain-at-alias", stderr)
		if allocs == 0 || allocs != frees || live != 0 {
			t.Errorf("allocs=%d frees=%d live_bytes=%d, want allocs == frees and live_bytes 0", allocs, frees, live)
		}
	})

	// At xs's LAST mention the same binding is a MOVE instead: the retain
	// is elided and xs's exit dec elided with it (moves_local_at +
	// note_moved_elided), the pair cancellation native performs at this
	// site — so the moved shape must emit no inc at all.
	t.Run("elides-retain-at-move-alias", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux", "function main(): i32 { let xs: i32[] = [1, 2]; let ys = xs; return ys[0]; }")
		if rcIncSites(asm) > 0 {
			t.Errorf("expected NO retain at the move-alias (the source's last mention transfers); found __fern_rc_inc in emitted asm")
		}
	})

	// Emission: aliasing an array struct field also retains.
	t.Run("emits-retain-at-field-alias", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux", "struct H { items: i32[] } function main(): i32 { let h: H = H { items: [1, 2] }; let y = h.items; return y[0]; }")
		if rcIncSites(asm) == 0 {
			t.Errorf("expected a retain (__fern_rc_inc) at the struct-field array alias; not found in emitted asm")
		}
	})
}

// Reassigning an array slot `y = x`: values stay correct, the over-release
// detector stays 0, and the reassignment releases the old value and retains
// the new one when it is a second owner.
func TestSelfHostRcReassignX86_64(t *testing.T) {
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// Reassign one array slot to alias another: ys now sees xs's data.
		{"reassign-to-alias", "function main(): i32 { let xs: i32[] = [1, 2, 3]; let ys: i32[] = [4, 5, 6]; ys = xs; return ys[0] + ys[2]; }", 4},
		// The source alias stays readable after the reassignment.
		{"reassign-source-intact", "function main(): i32 { let xs: i32[] = [7, 8]; let ys: i32[] = [0, 0]; ys = xs; return xs[1] + ys[1]; }", 16},
		// Reassign to a fresh literal (no retain), old value released.
		{"reassign-to-fresh", "function main(): i32 { let xs: i32[] = [1, 2]; xs = [9, 9, 9]; return xs[2]; }", 9},
		// Over-release detector stays 0 across reassignments.
		{"reassign-no-underflow", "function main(): i32 { let xs: i32[] = [1, 2]; let ys: i32[] = [3, 4]; ys = xs; let zs: i32[] = [5, 6]; zs = ys; return __rc_underflow_count(); }", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}

	// Emission: `ys = xs` releases the old value, and retains the new ref
	// when ys must outlive an update of xs.
	t.Run("emits-retain-and-release", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux", rcReassignUpdateSrc)
		if rcIncSites(asm) == 0 {
			t.Errorf("expected a retain (__fern_rc_inc) for the reassigned alias")
		}
		if code, _ := cli.runX86(t, asm); code != 6 {
			t.Errorf("exited %d, want 6 (11 = the retain did nothing: .with wrote through ys, and the sweep over-released)", code)
		}
		// The scope-exit sweep calls __fern_arr_dec whether or not the rebind
		// releases ys's old buffer; only the census tells the two apart.
		stderr, code := hevRun(t, cli.runner, buildBin(t, cli.gcc, t.TempDir(), "reassign", cli.emit(t, "x86-64-linux", rcReassignUpdateSrc, "FERN_LEAKCHECK=1")))
		if code != 6 {
			t.Errorf("leakcheck build exited %d, want 6", code)
		}
		allocs, frees, live := parseLeakcheck(t, "emits-retain-and-release", stderr)
		if allocs == 0 || allocs != frees || live != 0 {
			t.Errorf("allocs=%d frees=%d live_bytes=%d, want allocs == frees and live_bytes 0: the rebind must release ys's old buffer", allocs, frees, live)
		}
	})
}

// Phase 1d (cont.): the function-exit dec sweep releases every array
// LOCAL at each return / fall-through (borrowed params are skipped); an
// array returned to the caller is retained so it survives the sweep,
// and a `let` on a path not taken is never released, since it was never
// bound. With free off this is observably a no-op on values; we check
// value-correctness across calls (incl. returning an array and passing
// a borrowed array), a clean over-release detector, and the emission of
// the release sweep. Mirrors
// docs/RC-PERCEUS-SELF-HOST-PORT.md Phase 1d.
func TestSelfHostRcExitSweepX86_64(t *testing.T) {
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// Return an array: retained past the exit sweep so the caller's
		// reference is live.
		{"return-array", "function make(): i32[] { let xs: i32[] = [1, 2, 3]; return xs; } function main(): i32 { let ys = make(); return ys[0] + ys[2]; }", 4},
		// Borrowed array param: not released by the callee's sweep, so it
		// stays usable in the caller; detector clean.
		{"borrowed-param", "function sum2(a: i32[]): i32 { return a[0] + a[1]; } function main(): i32 { let xs: i32[] = [7, 8]; let r = sum2(xs); return r + xs[0] + __rc_underflow_count(); }", 22},
		// A function with array locals + alias, called repeatedly: each
		// call balances inc (alias) against the exit sweep, so the
		// over-release detector stays 0.
		{"exit-sweep-no-underflow", "function f(): i32 { let xs: i32[] = [1, 2]; let ys = xs; return ys[0]; } function main(): i32 { let a = f(); let b = f(); let c = f(); return __rc_underflow_count(); }", 0},
		// An array local declared inside a not-taken branch is never
		// bound, so the exit sweep has nothing to release (no spurious
		// release), detector clean.
		{"branch-local-unbound", "function main(): i32 { let xs: i32[] = [5, 6]; if (xs[0] > 100) { let ys: i32[] = [1, 2]; return ys[0]; } return xs[1] + __rc_underflow_count(); }", 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}

	// Emission: a function with an array local releases it at exit (the
	// __fern_arr_dec sweep).
	t.Run("emits-exit-sweep", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux",
			"function main(): i32 { let xs: i32[] = [1, 2]; return xs[0]; }")
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
	boxedProbes(t)
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// Bare owned local returned: moved to caller, values intact, detector clean.
		{"move-bare-local", "function make(): i32[] { let xs: i32[] = [10, 20, 30]; return xs; } function main(): i32 { let ys = make(); return ys[0] + ys[2] + __rc_underflow_count(); }", 40},
		// Moved through a chain of callers (each return moves), detector clean.
		{"move-chained", "function mk(): i32[] { let xs: i32[] = [1, 2, 3]; return xs; } function relay(): i32[] { let a = mk(); return a; } function main(): i32 { let b = relay(); return b[0] + b[2] + __rc_underflow_count(); }", 4},
		// Move co-exists with a sibling array local that IS swept (only the
		// returned slot is excluded): sibling release keeps detector clean.
		{"move-with-sibling-sweep", "function make(): i32[] { let keep: i32[] = [9, 9]; let xs: i32[] = [5, 6, 7]; return xs; } function main(): i32 { let ys = make(); return ys[0] + ys[2] + __rc_underflow_count(); }", 12},
		// Returning a BORROWED param array is NOT a move (idx < n_params):
		// the caller still owns it, so it stays usable after the call.
		{"return-param-not-moved", "@noinline function pick(a: i32[]): i32[] { return a; } function main(): i32 { let xs: i32[] = [3, 4]; let ys = pick(xs); return ys[0] + xs[1] + __rc_underflow_count(); }", 7},
		// Churn: a builder whose result is moved out on every call must
		// still allow reclamation (alloc >> heap completes via the freelist).
		{"move-churn", "function build(n: i32): i32[] { let xs: i32[] = []; let i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs; } function main(): i32 { let k = 0; let s = 0; while (k < 200000) { let r = build(64); s = r[63]; k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}

	// Emission: returning a bare owned array local elides the retain inc
	// (no `call __fn___fern_rc_inc` for that path), whereas returning a
	// non-ident array expression (a field access) still emits it.
	t.Run("emits-no-inc-on-move", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux",
			"function make(): i32[] { let xs: i32[] = [1, 2, 3]; return xs; } function main(): i32 { let ys = make(); return ys[0]; }")
		if rcIncSites(asm) > 0 {
			t.Errorf("move-on-return should elide the retain inc, but found call __fn___fern_rc_inc")
		}
	})
	t.Run("emits-inc-when-not-moved", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux",
			"struct H { items: i32[] } function get(h: H): i32[] { return h.items; } function main(): i32 { let hh: H = H { items: [4, 5, 6] }; let ys = get(hh); return ys[0]; }")
		if rcIncSites(asm) == 0 {
			t.Errorf("returning a non-local array expression should still emit the retain inc")
		}
	})
}

// Phase 1d arm64 parity: the array inc/dec wiring (alias retain,
// reassign-inc + dec-on-overwrite, function-exit release sweep +
// array-return retain) on the arm64 emitter. Run
// under qemu-aarch64. Value-correctness (free off → RC is a no-op on
// values) + a clean over-release detector across the lifecycle.
func TestSelfHostRcArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"alias-read-both", "function main(): i32 { let xs: i32[] = [10, 20, 30]; let ys = xs; return ys[1] + xs[0]; }", 30},
		{"reassign-to-alias", "function main(): i32 { let xs: i32[] = [1, 2, 3]; let ys: i32[] = [4, 5, 6]; ys = xs; return ys[0] + ys[2]; }", 4},
		{"field-alias", "struct H { items: i32[] } function main(): i32 { let h: H = H { items: [11, 22, 33] }; let y = h.items; return y[1] + h.items[2]; }", 55},
		{"return-array", "function make(): i32[] { let xs: i32[] = [1, 2, 3]; return xs; } function main(): i32 { let ys = make(); return ys[0] + ys[2]; }", 4},
		{"borrowed-param", "function sum2(a: i32[]): i32 { return a[0] + a[1]; } function main(): i32 { let xs: i32[] = [7, 8]; let r = sum2(xs); return r + xs[0] + __rc_underflow_count(); }", 22},
		{"exit-sweep-no-underflow", "function f(): i32 { let xs: i32[] = [1, 2]; let ys = xs; return ys[0]; } function main(): i32 { let a = f(); let b = f(); let c = f(); return __rc_underflow_count(); }", 0},
		{"branch-local-unbound", "function main(): i32 { let xs: i32[] = [5, 6]; if (xs[0] > 100) { let ys: i32[] = [1, 2]; return ys[0]; } return xs[1] + __rc_underflow_count(); }", 6},
		// Cow-aware dec (Phase 3 prep): a self-append loop stays clean.
		{"self-append-no-underflow", "function main(): i32 { let xs: i32[] = []; let i = 0; while (i < 20) { xs = xs.append(i); i = i + 1; } return __rc_underflow_count(); }", 0},
		{"self-append-values", "function main(): i32 { let xs: i32[] = []; let i = 0; while (i < 20) { xs = xs.append(i * 2); i = i + 1; } return xs[19]; }", 38},
		// Construction store: struct field + array-of-arrays capture.
		{"struct-holds-array", "struct H { items: i32[] } function mk(): H { let xs: i32[] = [7, 8]; return H { items: xs }; } function main(): i32 { let h = mk(); return h.items[0] + h.items[1] + __rc_underflow_count(); }", 15},
		// The inner arrays go through id so they are built on the heap rather than
		// placed as constants.
		{"array-of-arrays", "@noinline function id(xs: i32[]): i32[] { return xs; } function main(): i32 { let a: i32[] = id([1, 2]); let b: i32[] = id([3, 4]); let both: i32[][] = [a, b]; return both[0][1] + both[1][0] + __rc_underflow_count(); }", 5},
		{"struct-update-copy", "struct H { items: i32[], n: i32 } function main(): i32 { let xs: i32[] = [1, 2]; let h: H = H { items: xs, n: 0 }; let h2: H = H { ...h, n: 5 }; return h2.items[1] + h2.n + __rc_underflow_count(); }", 7},
		// Phase 3 (arm64 free): reclamation churn (alloc >> heap completes) + enum payload retain.
		// xs goes through id so the payload is built on the heap rather than placed as a constant.
		{"reclaim-churn", "function work(n: i32): i32 { let xs: i32[] = []; let i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs[n - 1]; } function main(): i32 { let k = 0; let s = 0; while (k < 200000) { s = work(200); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 3},
		{"enum-holds-array", "enum Box { Arr(i32[]), Empty } function id(xs: i32[]): i32[] { return xs; } function mk(): Box { let xs: i32[] = id([3, 4, 5]); return Arr(xs); } function main(): i32 { let b = mk(); match (b) { Arr(a) => { return a[1] + a[2] + __rc_underflow_count(); }, Empty => { return 0; } } }", 9},
		// Phase 4 (move-on-return): bare owned local moved to caller; sibling
		// local still swept; borrowed-param return is not a move.
		{"move-bare-local", "function make(): i32[] { let xs: i32[] = [10, 20, 30]; return xs; } function main(): i32 { let ys = make(); return ys[0] + ys[2] + __rc_underflow_count(); }", 40},
		{"move-with-sibling-sweep", "function make(): i32[] { let keep: i32[] = [9, 9]; let xs: i32[] = [5, 6, 7]; return xs; } function main(): i32 { let ys = make(); return ys[0] + ys[2] + __rc_underflow_count(); }", 12},
		{"return-param-not-moved", "@noinline function pick(a: i32[]): i32[] { return a; } function main(): i32 { let xs: i32[] = [3, 4]; let ys = pick(xs); return ys[0] + xs[1] + __rc_underflow_count(); }", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.exit {
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
	cli := newStrictCLI(t)
	cases := []struct {
		name string
		src  string
		exit int
	}{
		// A 20-iteration self-append loop: in-place growth returns the
		// same buffer, so the cow-aware dec keeps the detector at 0.
		{"self-append-no-underflow", "function main(): i32 { let xs: i32[] = []; let i = 0; while (i < 20) { xs = xs.append(i); i = i + 1; } return __rc_underflow_count(); }", 0},
		// Values stay correct across the self-mutation.
		{"self-append-values", "function main(): i32 { let xs: i32[] = []; let i = 0; while (i < 20) { xs = xs.append(i * 2); i = i + 1; } return xs[19]; }", 38},
		// A genuine reassignment to a different buffer still releases the
		// old and keeps the source readable, detector clean.
		{"reassign-different-clean", "function main(): i32 { let xs: i32[] = [1, 2]; let ys: i32[] = [3, 4]; ys = xs; return ys[0] + xs[1] + __rc_underflow_count(); }", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// A struct field initialised from an rc-tracked array: the struct's reference
// is counted, so a struct outliving the source local does not dangle. The
// field init retains when the source stays live and moves at its last use.
func TestSelfHostRcConstructX86_64(t *testing.T) {
	boxedProbes(t)
	cli := newStrictCLI(t)
	cases := []struct {
		name string
		src  string
		exit int
	}{
		// Struct captures an array alias; both readable, detector clean.
		{"struct-holds-array", "struct H { items: i32[] } function main(): i32 { let xs: i32[] = [1, 2, 3]; let h: H = H { items: xs }; return h.items[1] + xs[0] + __rc_underflow_count(); }", 3},
		// The capture survives the source local going out of scope (the
		// case that would UAF once free is on without the construction inc).
		{"struct-outlives-source", "struct H { items: i32[] } function mk(): H { let xs: i32[] = [7, 8]; return H { items: xs }; } function main(): i32 { let h = mk(); return h.items[0] + h.items[1] + __rc_underflow_count(); }", 15},
		// A struct field from a fresh literal is owned — not re-incremented.
		{"struct-fresh-literal", "struct H { items: i32[] } function main(): i32 { let h: H = H { items: [4, 5, 6] }; return h.items[2] + __rc_underflow_count(); }", 6},
		// Struct-update copies the base's array field (retained for soundness).
		{"struct-update-copy", "struct H { items: i32[], n: i32 } function main(): i32 { let xs: i32[] = [1, 2]; let h: H = H { items: xs, n: 0 }; let h2: H = H { ...h, n: 5 }; return h2.items[1] + h2.n + __rc_underflow_count(); }", 7},
		{"struct-update-override", "struct H { items: i32[], n: i32 } function main(): i32 { let xs: i32[] = [9, 8]; let h: H = H { items: [0], n: 1 }; let h2: H = H { ...h, items: xs }; return h2.items[0] + __rc_underflow_count(); }", 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
	// Emission: a struct field from an array ALIAS retains — `xs` is read after
	// the construction, so the box genuinely gains a second owner and the retain
	// is what keeps the two drops balanced.
	//
	// The probe reads `xs[1]` back for that reason. Without it the local is at
	// its LAST USE, which is a MOVE rather than an alias: #6726 hands the
	// reference to the box and elides both the retain and the sweep dec that
	// used to cancel it, so this assertion would be pinning the redundant pair
	// it was written before anyone noticed.
	t.Run("emits-retain-at-field-init", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux", "struct H { items: i32[] } function main(): i32 { let xs: i32[] = [1, 2]; let h: H = H { items: xs }; return h.items[0] + xs[1]; }")
		if rcIncSites(asm) == 0 {
			t.Errorf("expected a retain (__fern_rc_inc) at the struct field init of an aliased local")
		}
	})

	// The other half of that rule: the same construction over a local at its
	// LAST use is a move, and emits no retain at all. The field is read in a
	// callee that borrows h, so no field read in main can retain either.
	t.Run("no-retain-when-the-field-init-moves", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux", "struct H { items: i32[] }\n@noinline\nfunction first(h: H): i32 { return h.items[0]; }\n"+
			"function main(): i32 { let xs: i32[] = [1, 2]; let h: H = H { items: xs }; return first(h); }")
		if rcIncSites(asm) > 0 {
			t.Errorf("a moved local needs no retain at the field init — the box takes over its reference (#6726)")
		}
	})
}

// Phase 1d (array/tuple construction store): an array/tuple element
// initialised from an rc-tracked array alias retains the buffer (the
// container owns a new reference) — the array-literal and tuple-literal
// arms of the free-readiness gate. inc-only / detector-clean / safe.
func TestSelfHostRcConstructContainersX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	cases := []struct {
		name string
		src  string
		exit int
	}{
		// Array of arrays: the inner array aliases are retained. The inner arrays go
		// through id so they are built on the heap rather than placed as constants.
		{"array-of-arrays", "@noinline function id(xs: i32[]): i32[] { return xs; } function main(): i32 { let a: i32[] = id([1, 2]); let b: i32[] = id([3, 4]); let both: i32[][] = [a, b]; return both[0][1] + both[1][0] + __rc_underflow_count(); }", 5},
		// Tuple holding an array: the array element is retained.
		{"tuple-of-array", "function main(): i32 { let xs: i32[] = [7, 8]; let t = (xs, 9); return t.0[1] + t.1 + __rc_underflow_count(); }", 17},
		// Returning a container that captured a local array (would UAF
		// once free is on without the construction inc) stays correct. a goes
		// through id so it is built on the heap rather than placed as a constant.
		{"return-arr-of-arrs", "@noinline function id(xs: i32[]): i32[] { return xs; } function mk(): i32[][] { let a: i32[] = id([5, 6]); return [a, a]; } function main(): i32 { let both = mk(); return both[0][0] + both[1][1] + __rc_underflow_count(); }", 11},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// Phase 1d (closure capture): a lambda capturing an rc-tracked array
// retains the buffer (the closure box owns the reference). Uses the
// block-form lambda (`(): T => { ... }`); the arrow form
// `() => e` capturing a local is a separate pre-existing self-host
// limitation. inc-only / detector-clean / safe (free off).
func TestSelfHostRcClosureX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	cases := []struct {
		name string
		src  string
		exit int
	}{
		// Local closure capturing an array local.
		{"closure-captures-array", "function main(): i32 { let xs: i32[] = [3, 4, 5]; let f = (): i32 => { return xs[1] + xs[2]; }; return f() + __rc_underflow_count(); }", 9},
		// Closure escaping its defining function, capturing an array.
		{"closure-escapes-with-array", "function mk(xs: i32[]): () => i32 { return (): i32 => { return xs[0] + xs[1]; }; } function main(): i32 { let a: i32[] = [3, 4]; let f = mk(a); return f() + __rc_underflow_count(); }", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// Phase 3: free is ON for arrays (x86-64) -- buffers reclaimed via a
// size-class freelist + __fern_arr_dec at rc==1. Reclamation proof + the
// enum-payload retain that closed the JSON nested-structure gap.
func TestSelfHostRcFreeReclaimX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	cases := []struct {
		name string
		src  string
		exit int
	}{
		// Total allocation far exceeds the 1 GiB bump heap; completes
		// (exit 0) only because freed buffers are reused. (n<256 avoids a
		// pre-existing, unrelated .append grow bug.)
		{"reclaim-churn", "function work(n: i32): i32 { let xs: i32[] = []; let i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs[n - 1]; } function main(): i32 { let k = 0; let s = 0; while (k < 500000) { s = work(200); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 3},
		// Borrowed-param builder: callee must not free the caller's buffer.
		{"borrowed-param-builder", "function add(toks: i32[], t: i32): i32[] { return toks.append(t); } function main(): i32 { let ts: i32[] = []; let i = 0; while (i < 200) { ts = add(ts, i); i = i + 1; } return ts[199] + __rc_underflow_count(); }", 199},
		// Enum payload holding an array: the variant retains it, so the
		// source local going out of scope does not free it (would UAF
		// once free is on -- the JSON nested-structure gap). xs goes through id
		// so the payload is built on the heap rather than placed as a constant.
		{"enum-holds-array", "enum Box { Arr(i32[]), Empty } function id(xs: i32[]): i32[] { return xs; } function mk(): Box { let xs: i32[] = id([3, 4, 5]); return Arr(xs); } function main(): i32 { let b = mk(); match (b) { Arr(a) => { return a[1] + a[2] + __rc_underflow_count(); }, Empty => { return 0; } } }", 9},
		// Loop-local array rebind: `let r = build(n)` re-bound each iteration
		// is released per-iteration (StmtVar cow-guarded dec-on-overwrite),
		// not leaked until function exit. 100k rebinds stay value-correct and
		// over-release-detector clean.
		{"loop-local-rebind", "function build(n: i32): i32[] { let xs: i32[] = []; let i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs; } function main(): i32 { let s = 0; let k = 0; while (k < 100000) { let r: i32[] = build(32); s = s + r[31]; k = k + 1; } return (s % 5) + __rc_underflow_count(); }", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
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
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// A reclaimable struct with a struct-array field built from a FRESH
		// literal (sole owner): dropped 2000x, the field buffer is reclaimed
		// each time, detector clean.
		{"struct-arr-field-fresh-no-underflow", "struct E { v: i32 } struct H { es: E[] } function step(n: i32): i32 { let h = H { es: [E { v: n }, E { v: n + 1 }] }; return h.es[0].v; } function main(): i32 { let s = 0; let i = 0; while (i < 2000) { s = s + step(i); i = i + 1; } return s - s + __rc_underflow_count(); }", 0},
		// Struct-array field ALIASED from a borrowed param ident: construction
		// incs the buffer, the struct's reclamation field-drop decs it, the
		// borrowed param is not swept — balanced, detector clean across 2000 calls.
		// NOTE: the helper must not be named `use` — that became a reserved
		// keyword (the `use x <- call();` monadic bind, #4335/#4450) after this
		// case landed, and the self-host parser then silently miscompiled the
		// program into an infinite loop (the `i = i + 1` increment lowered to
		// StmtUnknown), hanging the CI shard at the 18m go-test timeout. The
		// silent-miscompile-on-parse-error bug is tracked in #4471. The first
		// element reads v off the heap so shared is built on the heap rather than
		// placed as a constant.
		{"struct-arr-field-alias-no-underflow", "struct E { v: i32 } struct H { es: E[] } function id(xs: i32[]): i32[] { return xs; } function wrapH(src: E[]): i32 { let h = H { es: src }; return h.es[0].v; } function main(): i32 { let shared: E[] = [E { v: id([3])[0] }, E { v: 4 }]; let s = 0; let i = 0; while (i < 2000) { s = s + wrapH(shared); i = i + 1; } return s - s + __rc_underflow_count(); }", 0},
		// Struct-array field from a fresh CALL value (sole owner, no inc): the
		// field-drop frees it; a non-fresh callee would over-free here.
		{"struct-arr-field-callvalue-no-underflow", "struct E { v: i32 } struct H { es: E[] } function mk(n: i32): E[] { return [E { v: n }, E { v: n * 2 }]; } function step(n: i32): i32 { let h = H { es: mk(n) }; return h.es[1].v; } function main(): i32 { let s = 0; let i = 0; while (i < 2000) { s = s + step(i); i = i + 1; } return s - s + __rc_underflow_count(); }", 0},
		// Array-of-ENUM field: the buffer is reclaimed the same shallow way.
		{"enum-arr-field-no-underflow", "enum K { A(i32), B } struct G { ks: K[] } function step(n: i32): i32 { let g = G { ks: [A(n), B] }; return match (g.ks[0]) { A(x) => x, B => 0 }; } function main(): i32 { let s = 0; let i = 0; while (i < 2000) { s = s + step(i); i = i + 1; } return s - s + __rc_underflow_count(); }", 0},
		// Value-correctness: the reclamation does not disturb the field reads
		// before the drop. The first element reads v off the heap so h is built on
		// the heap rather than placed as a constant.
		{"struct-arr-field-value", "struct E { v: i32 } struct H { es: E[] } function id(xs: i32[]): i32[] { return xs; } function main(): i32 { let h = H { es: [E { v: id([5])[0] }, E { v: 9 }] }; return h.es[0].v * 10 + h.es[1].v; }", 59},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}

	// Emission: a reclaimable struct whose ONLY rc field is a struct-array
	// releases that field's buffer (__fern_arr_dec) at the struct's reclamation,
	// AND deep-drops the element boxes — the walk is gated on __fern_rc_is_unique
	// (free the elements only when this drop frees the buffer, i.e. the sole
	// owner), then rc_dec's each element before the buffer dec.
	//
	// The binding is REASSIGNED, and that is essential rather than
	// incidental. With a single `let h = ...`, the reclaim this asserts on is
	// the rebind over h's slot while that slot still holds its prologue zero —
	// `is_unique(null)` is 0 on every path, so ir.fern's prune_zero_slot_guards
	// resolves the gate to a constant and the fold deletes the arm it gated,
	// correctly. A second assignment gives the reclaim a slot with a real
	// previous value, which is the case the gate exists for. Each v is read off
	// the heap so h is built on the heap rather than placed as a constant.
	t.Run("emits-struct-array-field-drop", func(t *testing.T) {
		asm := cli.emit(t, "x86-64-linux",
			"struct E { v: i32 } struct H { es: E[] } function id(xs: i32[]): i32[] { return xs; } function main(): i32 { let h = H { es: [E { v: id([1])[0] }] }; h = H { es: [E { v: id([2])[0] }] }; return h.es[0].v; }")
		// Read off H's drop helper alone: the program's own temporaries release
		// through the same helper calls.
		body := asmFuncBody(t, asm, "__fn___sem_drop_H")
		if n := strings.Count(body, "call __fn___fern_arr_dec"); n < 2 {
			t.Errorf("expected H's drop to release each element and then the buffer (__fern_arr_dec at least twice), got %d:\n%s", n, body)
		}
		if rcIsUniqueSites(body) == 0 {
			t.Errorf("expected the element-walk sole-owner gate in H's drop; not found:\n%s", body)
		}
	})
}
