package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #8429: a frame may not transfer a reference it BORROWED.
//
// A declared `own` position consumes one reference, and this compiler has no
// caller-side retain — a parameter that is not itself `own` is a borrow the
// caller still holds. #4873's self-reassign admission lets such a name reach an
// `own` position anyway (`h = absorb(h, …)` inside a plain-receiver method
// kills THIS binding, not the caller's), so the callee's
// __fern_rc_is_unique gate read the caller's sole count as its own and rewrote
// the caller's box in place. `var keep = h; var forked = h.update(c)` left
// keep == forked on the self-host while `-interp` and native forked correctly.
//
// The lowering now buys the reference the callee is about to spend
// (emit_own_borrowed_param_arg), so the guard sees rc >= 2 and degrades to a
// fresh box. The results check values and __rc_underflow_count(). Each expected
// result is pinned independently of the interpreter, so agreement cannot hide
// both engines returning the same wrong value.
//
// The controls are the leak direction, which no exit code reports: an `own`
// source and a dying local source are both already owned by the frame, and a
// retain there would strand a box per call. TestSelfHostOwnBorrowedParamArgLeakCheck
// runs every row against native's verdict.
var ownBorrowedParamArgCases = []struct {
	name     string
	src      string
	expected int
	// selfHostLeaks pins what the self-host's FERN_LEAKCHECK verdict IS, since
	// native is clean on every row and the two disagree. A `true` here is this
	// compiler's pre-existing main-exit gap — `main`'s struct locals are never
	// swept, so every fork row strands its boxes before this change and after
	// it. The rows that matter are the `false` ones: a retain emitted where the
	// frame already owns its value strands one box per call, and those two rows
	// are where that would land.
	selfHostLeaks bool
}{
	// The reported shape, minimised: a plain-receiver method threads its
	// receiver through an `own` consumer while the caller keeps two names on
	// the box. Before the fix all three read n == 1 (111); the fork is 100.
	{name: "receiver-fork-under-alias", expected: 100, selfHostLeaks: true, src: `struct S { buf: u8[], n: i32 }
@noinline
function zero(n: i32): u8[] {
    var a: u8[] = __alloc_u8(n);
    var i: i32 = 0;
    while (i < n) { a = a.with(i, 0 as u8); i = i + 1; }
    return a;
}
@noinline
function bump(own s: S): S { s = S { ...s, n: s.n + 1 }; return s; }
function (s: S) mbump(): S { s = bump(s); return s; }
function main(): i32 {
    var a: S = S { buf: zero(4), n: 0 };
    var keep: S = a;
    var b: S = a.mbump();
    return b.n * 100 + keep.n * 10 + a.n + __rc_underflow_count();
}`},

	// The same fork with NO second name: `a` alone stays live across the call,
	// which is enough — the count the callee reads is the caller's one
	// reference either way.
	{name: "receiver-fork-single-name", expected: 10, selfHostLeaks: true, src: `struct S { buf: u8[], n: i32 }
@noinline
function zero(n: i32): u8[] {
    var a: u8[] = __alloc_u8(n);
    var i: i32 = 0;
    while (i < n) { a = a.with(i, 0 as u8); i = i + 1; }
    return a;
}
@noinline
function bump(own s: S): S { s = S { ...s, n: s.n + 1 }; return s; }
function (s: S) mbump(): S { s = bump(s); return s; }
function main(): i32 {
    var a: S = S { buf: zero(4), n: 0 };
    var b: S = a.mbump();
    return b.n * 10 + a.n + __rc_underflow_count();
}`},

	// The hasher the issue was split from: a u64 counter carried through 100
	// rebinds and then forked. `keep` must still hold the pre-fork total, so
	// the difference is 1 and not 0.
	{name: "hasher-state-fork", expected: 7, selfHostLeaks: true, src: `struct St { a: u32, buf: u8[], buf_len: i32, total: u64 }
@noinline
function zero(n: i32): u8[] {
    var a: u8[] = __alloc_u8(n);
    var i: i32 = 0;
    while (i < n) { a = a.with(i, 0 as u8); i = i + 1; }
    return a;
}
@noinline
function absorb(own h: St, n: i32): St {
    h = St { ...h, buf_len: (h.buf_len + n) % 64, total: h.total + (n as u64) };
    return h;
}
function (h: St) update(n: i32): St { h = absorb(h, n); return h; }
function main(): i32 {
    var h: St = St { a: 1, buf: zero(64), buf_len: 0, total: 0 as u64 };
    var i: i32 = 0;
    while (i < 100) { h = h.update(7); i = i + 1; }
    var keep: St = h;
    var forked: St = h.update(7);
    return (forked.total as i32) - (keep.total as i32) + __rc_underflow_count();
}`},

	// A struct with an ARRAY field, aliased and then consumed-updated through
	// two frames. The array is untouched by the update — the box is what forks
	// — so this row isolates the box reuse from the field-own-move next door.
	// The aggregate rows compare full state and return a portable 0/1 status.
	// Their former large checksums were truncated by native process exits, but
	// rejected by WASI. Do not mask the checksum: that could hide a wrong field.
	{name: "array-field-struct-fork", expected: 0, selfHostLeaks: true, src: `struct P { xs: i32[], k: i32 }
@noinline
function mk(n: i32): i32[] {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < n) { a = a.append(0); i = i + 1; }
    return a;
}
@noinline
function grow(own p: P): P { p = P { ...p, k: p.k + 1 }; return p; }
function (p: P) g(): P { p = grow(p); return p; }
function main(): i32 {
    var a: P = P { xs: mk(3), k: 0 };
    var keep: P = a;
    var b: P = a.g();
    if (b.k != 1 || keep.k != 0 || a.k != 0 || keep.xs.len() != 3 || __rc_underflow_count() != 0) { return 1; }
    return 0;
}`},

	// A struct with a STRING field, same shape. Correct before the fix (the
	// string limb of the alias ladder already retains at the bind), and here so
	// the retain landing on the wrong side shows up as a leak rather than
	// silently.
	{name: "string-field-struct-fork", expected: 0, selfHostLeaks: true, src: `struct T { name: string, n: i32 }
@noinline
function cat(own s: string, x: string): string { s = s + x; return s; }
@noinline
function bump(own t: T): T { t = T { ...t, name: cat(t.name, "b"), n: t.n + 1 }; return t; }
function (t: T) mb(): T { t = bump(t); return t; }
function main(): i32 {
    var a: T = T { name: "a", n: 0 };
    var keep: T = a;
    var b: T = a.mb();
    if (b.n != 1 || keep.n != 0 || a.n != 0 || keep.name != "a" || a.name != "a" || b.name != "ab" || __rc_underflow_count() != 0) { return 1; }
    return 0;
}`},

	// Control, and the perf-shaped one: the pure REBIND idiom with no alias.
	// The retain still fires (the frame still borrows), so the reuse guard sees
	// rc 2 and forks a box per call — the documented cost of this compiler's
	// missing caller-side retain. It must stay CORRECT and must not leak.
	{name: "rebind-loop-no-alias", expected: 50, selfHostLeaks: true, src: `struct S { buf: u8[], n: i32, total: u64 }
@noinline
function zero(n: i32): u8[] {
    var a: u8[] = __alloc_u8(n);
    var i: i32 = 0;
    while (i < n) { a = a.with(i, 0 as u8); i = i + 1; }
    return a;
}
@noinline
function absorb(own s: S, k: i32): S { s = S { ...s, n: s.n + k, total: s.total + (k as u64) }; return s; }
function (s: S) upd(k: i32): S { s = absorb(s, k); return s; }
function main(): i32 {
    var h: S = S { buf: zero(8), n: 0, total: 0 as u64 };
    var i: i32 = 0;
    while (i < 50) { h = h.upd(1); i = i + 1; }
    return h.n + (h.total as i32) - 50 + __rc_underflow_count();
}`},

	// Control: the source is this frame's own `own` parameter. The caller
	// transferred it (E051), so the frame HOLDS a reference and passing it on
	// is a move — a retain here strands one box per call.
	{name: "own-param-source-control", expected: 12, src: `struct S { xs: i32[], n: i32 }
@noinline
function bump(own s: S): S { return S { ...s, n: s.n + 1 }; }
@noinline
function step(own s: S): S { s = bump(s); return s; }
function main(): i32 {
    var a: S = S { xs: [1], n: 0 };
    a = step(a);
    a = step(a);
    return a.n + a.xs.len() * 10 + __rc_underflow_count();
}`},

	// Control: the source is a plain LOCAL dying at its own self-reassign — the
	// #4873 shape the admission was written for. The frame owns the value, so
	// this stays a move and the in-place reuse survives.
	{name: "dying-local-source-control", expected: 12, src: `struct S { xs: i32[], n: i32 }
@noinline
function bump(own s: S): S { return S { ...s, n: s.n + 1 }; }
function main(): i32 {
    var a: S = S { xs: [1], n: 0 };
    a = bump(a);
    a = bump(a);
    return a.n + a.xs.len() * 10 + __rc_underflow_count();
}`},

	// Control: a SCALAR parameter at an `own` position. Nothing rc-tracked is
	// handed over, and a retain on an i32 is a compiler bug that SIGSEGVs
	// rather than no-opping (see the rc_inc guard note in irlower).
	{name: "scalar-own-param-control", expected: 3, src: `struct S { xs: i32[], n: i32 }
@noinline
function twice(own n: i32): i32 { n = n * 2; return n; }
function (n: i32) t(): i32 { n = twice(n); return n; }
function main(): i32 {
    var a: S = S { xs: [1], n: 3 };
    var k: i32 = a.n;
    return k.t() - 4 + a.xs.len() + __rc_underflow_count();
}`},
}

// TestSelfHostOwnBorrowedParamArgX86_64 — the production x86-64 IR path against
// the interpreter oracle. The three fork rows dissent before the fix.
func TestSelfHostOwnBorrowedParamArgX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range ownBorrowedParamArgCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != tc.expected {
				t.Fatalf("interpreter returned %d, want specified result %d", want, tc.expected)
			}
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "obpa_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostOwnBorrowedParamArgArm64 — the same cases through the arm64 emit.
// The retain comes out of shared irlower analysis rather than per-backend
// emission, so this leg is what catches it landing on one register backend.
func TestSelfHostOwnBorrowedParamArgArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range ownBorrowedParamArgCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != tc.expected {
				t.Fatalf("interpreter returned %d, want specified result %d", want, tc.expected)
			}
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "obpa_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostOwnBorrowedParamArgWasmIR — the wasm-IR leg, named in the issue as
// dissenting with the x86-64 one. It emits its own call-argument sequence.
func TestSelfHostOwnBorrowedParamArgWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range ownBorrowedParamArgCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != tc.expected {
				t.Fatalf("interpreter returned %d, want specified result %d", want, tc.expected)
			}
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("wasm driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "obpa_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			output, runErr := run.CombinedOutput()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q: %v\n%s", tc.name, runErr, output)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle): %v\n%s", tc.name, code, want, runErr, output)
			}
		})
	}
}

// TestSelfHostOwnBorrowedParamArgLeakCheck runs every case under FERN_LEAKCHECK,
// on BOTH compilers, and requires the self-host to match native's verdict.
//
// The exit-code legs above see only the wrong-answer direction. A retain landing
// where the frame already owns its value — the `own`-param and dying-local
// controls — strands one box per call instead: same answer, same counter, live
// bytes at exit.
func TestSelfHostOwnBorrowedParamArgLeakCheck(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range ownBorrowedParamArgCases {
		t.Run(tc.name, func(t *testing.T) {
			name := "obpaleak_" + tc.name
			natV, natExit := nativeLeakVerdict(t, cli, dir, name, tc.src)
			shV, shExit := selfHostLeakVerdict(t, gcc, runner, driverBin, dir, name, tc.src)
			if natV != verdictClean {
				t.Fatalf("native is not clean on %s (%s, exit %d) — the oracle moved, re-derive before touching the self-host", tc.name, natV, natExit)
			}
			if natExit != tc.expected {
				t.Fatalf("native returned %d under leakcheck, want specified result %d", natExit, tc.expected)
			}
			want := verdictClean
			if tc.selfHostLeaks {
				want = verdictLeak
			}
			if shV != want {
				t.Errorf("self-host %s: %s (exit %d), want %s", tc.name, shV, shExit, want)
			}
			if shExit != natExit {
				t.Errorf("self-host %s exited %d under leakcheck, native %d", tc.name, shExit, natExit)
			}
		})
	}
}
