package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var ownBorrowedParamArgCases = []struct {
	name     string
	src      string
	expected int
}{
	{name: "receiver-fork-under-alias", expected: 100, src: `struct S { buf: u8[], n: i32 }
@noinline
function zero(n: i32): u8[] {
    let a: u8[] = __alloc_u8(n);
    let i: i32 = 0;
    while (i < n) { a = a.with(i, 0 as u8); i = i + 1; }
    return a;
}
@noinline
function bump(own s: S): S { s = S { ...s, n: s.n + 1 }; return s; }
function (s: S) mbump(): S { s = bump(s); return s; }
function main(): i32 {
    let a: S = S { buf: zero(4), n: 0 };
    let keep: S = a;
    let b: S = a.mbump();
    return b.n * 100 + keep.n * 10 + a.n + __rc_underflow_count();
}`},

	{name: "receiver-fork-single-name", expected: 10, src: `struct S { buf: u8[], n: i32 }
@noinline
function zero(n: i32): u8[] {
    let a: u8[] = __alloc_u8(n);
    let i: i32 = 0;
    while (i < n) { a = a.with(i, 0 as u8); i = i + 1; }
    return a;
}
@noinline
function bump(own s: S): S { s = S { ...s, n: s.n + 1 }; return s; }
function (s: S) mbump(): S { s = bump(s); return s; }
function main(): i32 {
    let a: S = S { buf: zero(4), n: 0 };
    let b: S = a.mbump();
    return b.n * 10 + a.n + __rc_underflow_count();
}`},

	{name: "hasher-state-fork", expected: 7, src: `struct St { a: u32, buf: u8[], buf_len: i32, total: u64 }
@noinline
function zero(n: i32): u8[] {
    let a: u8[] = __alloc_u8(n);
    let i: i32 = 0;
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
    let h: St = St { a: 1, buf: zero(64), buf_len: 0, total: 0 as u64 };
    let i: i32 = 0;
    while (i < 100) { h = h.update(7); i = i + 1; }
    let keep: St = h;
    let forked: St = h.update(7);
    return (forked.total as i32) - (keep.total as i32) + __rc_underflow_count();
}`},

	{name: "array-field-struct-fork", expected: 0, src: `struct P { xs: i32[], k: i32 }
@noinline
function mk(n: i32): i32[] {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < n) { a = a.append(0); i = i + 1; }
    return a;
}
@noinline
function grow(own p: P): P { p = P { ...p, k: p.k + 1 }; return p; }
function (p: P) g(): P { p = grow(p); return p; }
function main(): i32 {
    let a: P = P { xs: mk(3), k: 0 };
    let keep: P = a;
    let b: P = a.g();
    if (b.k != 1 || keep.k != 0 || a.k != 0 || keep.xs.len() != 3 || __rc_underflow_count() != 0) { return 1; }
    return 0;
}`},

	{name: "string-field-struct-fork", expected: 0, src: `struct T { name: string, n: i32 }
@noinline
function cat(own s: string, x: string): string { s = s + x; return s; }
@noinline
function bump(own t: T): T { t = T { ...t, name: cat(t.name, "b"), n: t.n + 1 }; return t; }
function (t: T) mb(): T { t = bump(t); return t; }
function main(): i32 {
    let a: T = T { name: "a", n: 0 };
    let keep: T = a;
    let b: T = a.mb();
    if (b.n != 1 || keep.n != 0 || a.n != 0 || keep.name != "a" || a.name != "a" || b.name != "ab" || __rc_underflow_count() != 0) { return 1; }
    return 0;
}`},

	{name: "rebind-loop-no-alias", expected: 50, src: `struct S { buf: u8[], n: i32, total: u64 }
@noinline
function zero(n: i32): u8[] {
    let a: u8[] = __alloc_u8(n);
    let i: i32 = 0;
    while (i < n) { a = a.with(i, 0 as u8); i = i + 1; }
    return a;
}
@noinline
function absorb(own s: S, k: i32): S { s = S { ...s, n: s.n + k, total: s.total + (k as u64) }; return s; }
function (s: S) upd(k: i32): S { s = absorb(s, k); return s; }
function main(): i32 {
    let h: S = S { buf: zero(8), n: 0, total: 0 as u64 };
    let i: i32 = 0;
    while (i < 50) { h = h.upd(1); i = i + 1; }
    return h.n + (h.total as i32) - 50 + __rc_underflow_count();
}`},

	{name: "own-param-source-control", expected: 12, src: `struct S { xs: i32[], n: i32 }
@noinline
function bump(own s: S): S { return S { ...s, n: s.n + 1 }; }
@noinline
function step(own s: S): S { s = bump(s); return s; }
function main(): i32 {
    let a: S = S { xs: [1], n: 0 };
    a = step(a);
    a = step(a);
    return a.n + a.xs.len() * 10 + __rc_underflow_count();
}`},

	{name: "dying-local-source-control", expected: 12, src: `struct S { xs: i32[], n: i32 }
@noinline
function bump(own s: S): S { return S { ...s, n: s.n + 1 }; }
function main(): i32 {
    let a: S = S { xs: [1], n: 0 };
    a = bump(a);
    a = bump(a);
    return a.n + a.xs.len() * 10 + __rc_underflow_count();
}`},

	{name: "scalar-own-param-control", expected: 3, src: `struct S { xs: i32[], n: i32 }
@noinline
function twice(own n: i32): i32 { n = n * 2; return n; }
function (n: i32) t(): i32 { n = twice(n); return n; }
function main(): i32 {
    let a: S = S { xs: [1], n: 3 };
    let k: i32 = a.n;
    return k.t() - 4 + a.xs.len() + __rc_underflow_count();
}`},
}

func TestSelfHostOwnBorrowedParamArgX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range ownBorrowedParamArgCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != tc.expected {
				t.Fatalf("interpreter returned %d, want specified result %d", want, tc.expected)
			}
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src))
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

func TestSelfHostOwnBorrowedParamArgArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range ownBorrowedParamArgCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != tc.expected {
				t.Fatalf("interpreter returned %d, want specified result %d", want, tc.expected)
			}
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
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

func TestSelfHostOwnBorrowedParamArgWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range ownBorrowedParamArgCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want != tc.expected {
				t.Fatalf("interpreter returned %d, want specified result %d", want, tc.expected)
			}
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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

func TestSelfHostOwnBorrowedParamArgLeakCheck(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "driver")

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
			if shV != verdictClean {
				t.Errorf("self-host %s: %s (exit %d), want %s", tc.name, shV, shExit, verdictClean)
			}
			if shExit != natExit {
				t.Errorf("self-host %s exited %d under leakcheck, native %d", tc.name, shExit, natExit)
			}
		})
	}
}
