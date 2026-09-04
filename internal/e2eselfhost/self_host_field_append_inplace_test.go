package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	// The SOLE-OCCURRENCE death (#6048): `p` is read exactly once in bump3's
	// whole body, so the caller-side bracket skips it there — and the growth
	// escapes to bump3's OWN caller, which means the may-grow set has to
	// propagate through that shape as well as through the self-reassign one.
	{"sole-occurrence-pass-through", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl }; }
function bump(s: St, v: i32): St { return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl }; }
function bump3(p: St): St { var t: St = bump(p, 4); return t; }
function main(): i32 {
    var a: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    var e: St = bump3(a);
    return a.ops.len() * 10 + e.ops.len() + e.ops[5];
}`},
	// The bracket's release must name the buffer its retain named. A callee that
	// hands back the caller's own box — an empty pass-through — leaves that box
	// freed by the dying-donor release and handed straight back to the next
	// call's result-box allocation, so a release that re-reads `b.ops` reads the
	// buffer that call just grew (#8224).
	{"passthrough-then-bracketed-grow", `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 }; }
function passthru(n: i32, s: St): St {
    var t: St = s;
    var i: i32 = 0;
    while (i < n) { t = t.emit(i); i = i + 1; }
    return t;
}
function outer(a: St): i32 {
    var b: St = passthru(0, a);
    var c: St = b.emit(9);
    return b.ops.len() * 10 + c.ops.len() + c.ops[6];
}
function main(): i32 {
    var a: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 6) { a = a.emit(i); i = i + 1; }
    return outer(a);
}`},
	// A struct local bound from a FIELD READ is not a container this frame owns,
	// so the dying `aug = f(aug)` rebind must NOT be exempt from the bracket:
	// growing one of aug's array fields in place reaches through it into `sg`,
	// two levels deep, where the one-level may-grow-fields mask cannot follow.
	// The `.with` on one of the two co-indexed arrays is what makes the damage
	// visible rather than merely wrong — it re-clones `next` at cap == len, so
	// the next append reallocs `next` while `rows` still has spare capacity and
	// grows in place, and the container is left with two arrays one entry apart
	// (#8224; the shape is irlower's own `var aug = sg.struct_ret_fns`).
	{"field-read-alias-refuses-exemption", `
struct Reg { rows: i32[], next: i32[] }
struct Sigs { reg: Reg, tag: i32 }
function append_row(r: Reg, v: i32): Reg {
    var rows: i32[] = r.rows.append(v);
    var next: i32[] = r.next.append(0 - 1);
    next = next.with(0, v);
    return Reg { rows: rows, next: next };
}
function grow_from_field(sg: Sigs): i32 {
    var aug: Reg = sg.reg;
    var i: i32 = 0;
    while (i < 5) { aug = append_row(aug, i); i = i + 1; }
    if (sg.reg.rows.len() != 3) { return 71; }
    if (sg.reg.next.len() != 3) { return 72; }
    if (aug.rows.len() != 8) { return 73; }
    if (aug.next.len() != 8) { return 74; }
    return 7;
}
function main(): i32 {
    var rows: i32[] = [];
    var next: i32[] = [];
    var k: i32 = 0;
    while (k < 3) { rows = rows.append(k); next = next.append(0 - 1); k = k + 1; }
    var sg: Sigs = Sigs { reg: Reg { rows: rows, next: next }, tag: 0 };
    return grow_from_field(sg);
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

// The grow bracket must RELEASE WHAT IT RETAINED. Its release side used to be a
// second evaluation of the same PLACE — load the slot, walk the field hops, dec
// — and a place is not stable across the call it brackets: a box the caller
// still names can be freed by the dying-donor release of a pass-through call and
// handed straight back to the next callee's own result-box allocation, so the
// re-read yields the buffer that callee just grew. The dec then lands on a live
// rc-1 buffer, whose freed block takes a freelist link over its cap word, and
// the next append reads cap 0 with len N and copies N elements into a
// four-element buffer (#8224). No answer separates the two forms until the
// freelist happens to line up, so this reads the pairing off the asm.
func TestSelfHostGrowFieldBracketReleasesRetainedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const src = `
struct St { ops: i32[], ctrl: i32 }
function (s: St) emit(op: i32): St { return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl }; }
function outer(a: St): i32 {
    var b: St = a.emit(9);
    return a.ops.len() * 10 + b.ops.len();
}
function main(): i32 {
    var a: St = St { ops: [], ctrl: 0 };
    var i: i32 = 0;
    while (i < 5) { a = a.emit(i); i = i + 1; }
    return outer(a);
}`

	body := asmFuncBody(t, string(runCaptureStrictIR(t, gcc, runner, driverBin, []byte(src), "-ir")), "__fn_outer")
	lines := strings.Split(body, "\n")
	slotRe := regexp.MustCompile(`^\s*movq %rax, (-\d+\(%rbp\))$`)
	pushRe := regexp.MustCompile(`^\s*pushq (-\d+\(%rbp\))$`)

	held := ""
	for i, ln := range lines {
		if !strings.Contains(ln, "call __fn___fern_rc_inc") {
			continue
		}
		for j := i - 1; j >= 0 && j > i-6; j-- {
			if m := slotRe.FindStringSubmatch(lines[j]); m != nil {
				held = m[1]
				break
			}
		}
		break
	}
	if held == "" {
		t.Fatalf("no bracket retain capturing a slot in __fn_outer; body:\n%s", body)
	}
	for i, ln := range lines {
		if !strings.Contains(ln, "call __fn___fern_arr_dec") {
			continue
		}
		m := pushRe.FindStringSubmatch(lines[i-1])
		if m == nil {
			t.Fatalf("bracket release is not a plain load of a captured slot (got %q); body:\n%s", strings.TrimSpace(lines[i-1]), body)
		}
		if m[1] != held {
			t.Fatalf("bracket released %s but retained %s; body:\n%s", m[1], held, body)
		}
		return
	}
	t.Fatalf("no bracket release in __fn_outer; body:\n%s", body)
}
