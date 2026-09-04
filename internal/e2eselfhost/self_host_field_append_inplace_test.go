package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #8224: a struct-FIELD-receiver `.append` used to clone the whole array before
// growing the clone, so `S { ...s, xs: s.xs.append(v) }` — the state-threading
// shape every self-host emitter is built out of — was O(n) per append and O(n^2)
// per built array. field_append_inplace_sites_of now proves, per function, which
// of those appends no later read can observe and lets them grow the field's own
// buffer.
//
// The two halves are only sound together, so both are pinned here. The CALLEE
// half is the admission (the shape cases below). The CALLER half is the #4873
// grow bracket extended to a struct argument's array-field buffers: a callee
// that may grow them in place must not do so through a container the caller
// still reads. Every case here is differential — the expectation is the
// interpreter's answer, never a written-down number — because it is the ANSWER
// that diverges when either half is missing.
//
// SPARE CAPACITY is what makes an in-place grow possible at all, so each case
// threads its container through several appends before the read it checks.
var selfHostFieldAppendCases = []struct {
	name string
	src  string
}{
	// The measured shape: a receiver method returning a spread literal that
	// overrides the appended field. Threading it is the whole point, and the
	// answer must not change.
	{"spread-threading", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St {
    var nctrl: i32 = s.ctrl;
    if (op == 1) { nctrl = s.ctrl + 1; }
    return St { ...s, ops: s.ops.append(op), ctrl: nctrl };
}
function main(): i32 {
    var s: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 30) { s = s.emit(i); i = i + 1; }
    var sum: i32 = 0;
    for v in s.ops { sum = sum + v; }
    return s.ops.len() + s.ctrl + (sum % 7);
}`},
	// The caller-side hole through a METHOD receiver: `a` survives the call, so
	// its buffer must not grow under it.
	{"method-recv-survives", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl }; }
function main(): i32 {
    var a: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    var b: St = a.emit(9);
    return a.ops.len() * 10 + b.ops.len() + b.ops[5];
}`},
	// The same hole through a free function's struct PARAMETER.
	{"free-param-survives", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl }; }
function bump(s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl }; }
function main(): i32 {
    var a: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    var c: St = bump(a, 7);
    return a.ops.len() * 10 + c.ops.len() + c.ops[5];
}`},
	// Transitive: bump2 rebinds its own parameter from the call, which is the
	// dying shape the bracket exempts — so the growth escapes to ITS caller and
	// the may-grow flag has to propagate through the fixpoint.
	{"transitive-pass-through", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl }; }
function bump(s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl }; }
function bump2(s: St, v: i32): St { s = bump(s, v); return s; }
function main(): i32 {
    var a: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    var d: St = bump2(a, 6);
    return a.ops.len() * 10 + d.ops.len() + d.ops[5];
}`},
	// A struct argument reached through a FIELD chain: the bracket has to walk
	// the container's field hops to the inner struct's buffer.
	{"nested-field-argument", `
struct Inner { xs: i32[] }
struct Outer { inner: Inner, tag: i32 }
function push(i: Inner, v: i32): Inner { return Inner { xs: i.xs.append(v) }; }
function main(): i32 {
    var o: Outer = Outer { inner: Inner { xs: [] }, tag: 0 };
    var k: i32 = 0;
    while (k < 5) { o = Outer { ...o, inner: push(o.inner, k) }; k = k + 1; }
    var again: Inner = push(o.inner, 8);
    return o.inner.xs.len() * 10 + again.xs.len() + again.xs[5];
}`},
	// The same field READ AGAIN inside the literal the append feeds: the clone
	// must stay, or `n` reads the grown length.
	{"same-field-read-forces-clone", `
struct St { ops: i32[], n: i32 }
function grow(s: St, v: i32): St { return St { ops: s.ops.append(v), n: s.ops.len() }; }
function main(): i32 {
    var a: St = St { ops: [], n: 0 };
    var i: i32 = 0;
    while (i < 5) { a = St { ops: a.ops.append(i), n: a.n }; i = i + 1; }
    var b: St = grow(a, 9);
    return a.ops.len() * 10 + b.ops.len() + b.n;
}`},
	// A BARE read of the container in the same expression hands the whole thing
	// over, so the buffer stays readable through it.
	{"bare-read-forces-clone", `
struct St { ops: i32[], n: i32 }
function total(s: St): i32 { var t: i32 = 0; for v in s.ops { t = t + v; } return t; }
function grow(s: St, v: i32): St { return St { ops: s.ops.append(v), n: total(s) }; }
function main(): i32 {
    var a: St = St { ops: [], n: 0 };
    var i: i32 = 0;
    while (i < 5) { a = St { ops: a.ops.append(i), n: a.n }; i = i + 1; }
    var b: St = grow(a, 9);
    return a.ops.len() * 10 + b.ops.len() + b.n;
}`},
	// A pointer-element field: the grown buffer holds string boxes the container
	// still owns, so this pins the element retain the clone form also pays.
	{"string-field", `
struct Bag { names: string[], n: i32 }
function (b: Bag) add(s: string): Bag { return Bag { ...b, names: b.names.append(s), n: b.n + 1 }; }
function main(): i32 {
    var b: Bag = Bag { names: [], n: 0 };
    var i: i32 = 0;
    while (i < 6) { b = b.add("ab"); i = i + 1; }
    var c: Bag = b.add("cdef");
    var t: i32 = 0;
    for s in b.names { t = t + s.len(); }
    for s in c.names { t = t + s.len(); }
    return t + b.names.len() + c.names.len();
}`},
	// A local — not a parameter — as the container: its box is this frame's, so
	// the analysis must refuse and the clone must stay.
	{"local-container-refused", `
struct St { ops: i32[], n: i32 }
function main(): i32 {
    var a: St = St { ops: [], n: 0 };
    var i: i32 = 0;
    while (i < 5) { a = St { ops: a.ops.append(i), n: a.n }; i = i + 1; }
    var keep: St = a;
    var b: St = St { ops: a.ops.append(9), n: a.n };
    return keep.ops.len() * 10 + b.ops.len() + a.ops.len();
}`},
	// An i64[] field takes the 8-byte-slot push helper, which wasm dispatches
	// separately from the i32 one.
	{"i64-field", `
struct W { xs: i64[], n: i32 }
function (w: W) put(v: i64): W { return W { ...w, xs: w.xs.append(v), n: w.n + 1 }; }
function main(): i32 {
    var w: W = W { xs: [], n: 0 };
    var i: i32 = 0;
    while (i < 6) { w = w.put(3i64); i = i + 1; }
    var t: i64 = 0i64;
    for v in w.xs { t = t + v; }
    return (t as i32) + w.xs.len() + w.n;
}`},
}

// TestSelfHostFieldAppendInPlaceX86_64 — the production x86-64 IR path against
// the interpreter oracle.
func TestSelfHostFieldAppendInPlaceX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldAppendCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "fai_"+tc.name, string(asm))
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

// TestSelfHostFieldAppendInPlaceArm64 — the same cases through the arm64 emit.
// The decision is shared irlower analysis, so this leg guards the two register
// backends agreeing about the grow helper's uniqueness gate.
func TestSelfHostFieldAppendInPlaceArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldAppendCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "fai_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostFieldAppendInPlaceWasmIR — the wasm-IR leg, which reaches
// $__fern_arr_push_i64 for the 8-byte-slot field.
func TestSelfHostFieldAppendInPlaceWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR field-append e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldAppendCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(wat) == 0 {
				t.Fatal("self-host wasm compiler emitted 0 bytes")
			}
			watFile := filepath.Join(dir, "fai_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// The analysis itself, read off the emitted asm: an admitted site grows the
// field's own buffer (__fern_arr_push, no slice), a refused one still clones
// (__fern_arr_slice) before growing. Answers alone cannot separate these — the
// clone form computes the same result, just quadratically — so the shape is
// what pins that the decision went the intended way.
func TestSelfHostFieldAppendInPlaceShapeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cases := []struct {
		name      string
		src       string
		label     string
		wantClone bool
	}{
		{
			name: "spread-override-grows-in-place",
			src: `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 }; }
function main(): i32 { var s: St = St { ops: [], ctrl: 0 }; s = s.emit(1); return s.ops.len(); }`,
			label:     "__fn_St__emit",
			wantClone: false,
		},
		{
			name: "same-field-read-clones",
			src: `
struct St { ops: i32[], n: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), n: s.ops.len() }; }
function main(): i32 { var s: St = St { ops: [], n: 0 }; s = s.emit(1); return s.ops.len(); }`,
			label:     "__fn_St__emit",
			wantClone: true,
		},
		{
			name: "local-container-clones",
			src: `
struct St { ops: i32[], n: i32 }
function grow(v: i32): St { var a: St = St { ops: [], n: 0 }; return St { ...a, ops: a.ops.append(v) }; }
function main(): i32 { return grow(3).ops.len(); }`,
			label:     "__fn_grow",
			wantClone: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := string(runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir"))
			body := asmFuncBody(t, asm, tc.label)
			gotClone := strings.Contains(body, "__fern_arr_slice")
			if gotClone != tc.wantClone {
				t.Errorf("%s: clone-before-grow = %v, want %v; body:\n%s", tc.name, gotClone, tc.wantClone, body)
			}
			if !strings.Contains(body, "__fern_arr_push") {
				t.Errorf("%s: no arr_push in %s; body:\n%s", tc.name, tc.label, body)
			}
		})
	}
}
