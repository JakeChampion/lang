package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #8198: a struct field lifted into a local, superseded in the container, then
// moved into an `own` parameter came back with the value lost.
//
// The release is __field_reclaim_<T>, run when a struct local is rebound. It
// frees each rc field of the OLD box that the NEW one does not carry, and a
// nested struct / enum field deliberately SKIPS the carried (old.f == new.f)
// test: a field carried through `...base` was inc'd for the new box, so the old
// box's own claim has to die there or the inner box is stranded (#6605).
//
// That reasoning assumes pointer equality implies a second claim. It does not.
// A callee taking the field by `own` may reuse its box in place and hand it
// back, so old.f and new.f coincide with ONE reference between them, and the
// release frees a box the new binding names. The fix asks the rc: equal
// pointers skip the release only when the field is uniquely owned, which leaves
// the #6605 case (rc >= 2) releasing exactly as before.
//
// SILENT, and that is why a scale probe found it rather than a gate. The free
// is at rc 1, so __rc_underflow_count() never moves; the block goes to the
// freelist and the next allocation of that shape gets it back, so the lifted
// local and the fresh field become the same box. `rounds` is what exposes it:
// every round re-lifts a field that is empty again, so a broken compiler
// answers 1 no matter how many rounds run, and the count is the answer.
//
// Differential against `fern -interp` rather than written-down numbers.
var selfHostLiftSupersedeCases = []struct {
	name string
	src  string
}{
	// The reported shape: lift, supersede through a spread, move the local into
	// an `own` parameter, write it back. 8 rounds, so a compiler that loses the
	// lift answers 1.
	{"lift-supersede-own", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(own s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    var st: CfiState = a.cfi;\n    a = Asm { ...a, cfi: CfiState { bad: [], open: false } };\n    st = directive(st, v);\n    return Asm { ...a, cfi: st };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    var i: i32 = 0;\n    while (i < 8) { a = step(a, i); i = i + 1; }\n    if (!a.cfi.open) { return 70; }\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	// The same, with the supersede written as a full literal instead of a
	// spread. Both spellings lower to the same reclaim, and pinning both is
	// what says the fix is in the release rather than in one literal form.
	{"lift-supersede-own-full-literal", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(own s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    var st: CfiState = a.cfi;\n    a = Asm { cfi: CfiState { bad: [], open: false }, n: a.n };\n    st = directive(st, v);\n    return Asm { ...a, cfi: st };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    var i: i32 = 0;\n    while (i < 8) { a = step(a, i); i = i + 1; }\n    if (!a.cfi.open) { return 70; }\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	// Straight-line, no loop: two calls, expecting 2. The loop is not what
	// carries the defect, and a row that says so keeps a future fix from being
	// written as a loop-rotation special case.
	{"lift-supersede-own-straightline", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(own s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    var st: CfiState = a.cfi;\n    a = Asm { ...a, cfi: CfiState { bad: [], open: false } };\n    st = directive(st, v);\n    return Asm { ...a, cfi: st };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    a = step(a, 1);\n    a = step(a, 2);\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	// Control: no supersede — the rebind carries the same `cfi` pointer, so the
	// plain carried compare already spared it. Correct before the fix as well
	// as after; it pins that the uniq arm did not disturb the carried path.
	{"carried-field-control", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(own s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    var st: CfiState = a.cfi;\n    a = Asm { ...a, n: a.n + 1 };\n    st = directive(st, v);\n    return Asm { ...a, cfi: st };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    var i: i32 = 0;\n    while (i < 8) { a = step(a, i); i = i + 1; }\n    if (!a.cfi.open) { return 70; }\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	// Control: the callee BORROWS the field instead of owning it, so no box is
	// reused in place and old.f / new.f cannot coincide this way. Correct
	// before the fix too — the `own` position is half of what it takes.
	{"borrowed-callee-control", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    var st: CfiState = a.cfi;\n    a = Asm { ...a, cfi: CfiState { bad: [], open: false } };\n    st = directive(st, v);\n    return Asm { ...a, cfi: st };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    var i: i32 = 0;\n    while (i < 8) { a = step(a, i); i = i + 1; }\n    if (!a.cfi.open) { return 70; }\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	// Control: no call at all — the field is rebuilt inline, so nothing can
	// reuse its box. The other half of the pair.
	{"no-call-control", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    var st: CfiState = a.cfi;\n    a = Asm { ...a, cfi: CfiState { bad: [], open: false } };\n    st = CfiState { ...st, bad: st.bad.append(v), open: true };\n    return Asm { ...a, cfi: st };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    var i: i32 = 0;\n    while (i < 8) { a = step(a, i); i = i + 1; }\n    if (!a.cfi.open) { return 70; }\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},
}

// TestSelfHostLiftSupersedeOwnX86_64 — the production x86-64 IR path against
// the interpreter oracle. The three lift-supersede rows exit 1 without the fix.
func TestSelfHostLiftSupersedeOwnX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostLiftSupersedeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "lso_"+tc.name, string(asm))
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

// TestSelfHostLiftSupersedeOwnArm64 — the arm64 emit of the same helper. Each
// backend writes its own __field_reclaim_<T> body from the shared directive
// list, so a fix applied to one of them lands here as a failure.
func TestSelfHostLiftSupersedeOwnArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostLiftSupersedeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "lso_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostLiftSupersedeOwnWasmIR — the wasm leg, which is where #8198 was
// first seen. wasm_ir writes its reclaim body from its own per-field walk
// rather than the register backends' shared loop, so it is a third
// implementation of the same rule and needs its own row.
func TestSelfHostLiftSupersedeOwnWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range selfHostLiftSupersedeCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
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
			watFile := filepath.Join(dir, "lso_"+tc.name+".wat")
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
