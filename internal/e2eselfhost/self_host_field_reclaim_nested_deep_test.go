package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #8538: `__field_reclaim_<T>` released a replaced nested-STRUCT field with a
// shallow box dec, stranding whatever that inner box owned.
//
// The helper's per-field release was `__fern_arr_dec` for every field kind.
// Right for an array — the buffer IS the box — and wrong for a nested struct,
// which owns rc children of its own. Its sibling `__struct_drop_<T>` had the
// answer all along: gate on `is_unique(field)`, call `__struct_drop_<Inner>`,
// then dec the box. The fix is that sequence, after (not instead of) the guards
// that decide whether to release at all — the cow compare, the snapshot compare,
// and #8198's uniq test.
//
// THE EXIT CODE DOES NOT MOVE. This is a pure leak, so the register and wasm
// legs below are the miscompile guard on adding a deep walk inside a helper
// that three backends emit; the LEAKCHECK leg is the gate, with native as the
// oracle.
//
// A direct ENUM field with an rc payload leaked the same way; #8567 fixed it by
// giving all three backends a single-box variant walk (__enum_drop_) and calling
// it from both this helper and __struct_drop_. The scalar-payload enum row below
// is the boundary and predates that: nothing heap sits under that box, so the
// shallow dec is already complete and it must stay clean either way.
var selfHostFieldReclaimNestedCases = []struct {
	name string
	src  string
}{
	// The reported shape: a struct local rebound from a call, whose old value's
	// nested-struct field owns a buffer.
	{"nested-field-rebind-from-call", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { cfi: CfiState { bad: [v], open: false }, n: a.n };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    a = step(a, 1);\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	// The same with the write-back spelled as a spread — the spread is not the
	// condition, and this row is what says so.
	{"nested-field-rebind-spread", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, cfi: CfiState { bad: [v], open: false } };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    a = step(a, 1);\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	// Two levels of nesting. `__struct_drop_<Inner>` recurses, so one call at the
	// top reaches the buffer two boxes down — this row is what proves the fix is
	// a recursion rather than a single extra level.
	{"nested-two-deep", "struct Inner { xs: i32[] }\nstruct Mid { i: Inner }\nstruct Asm { m: Mid, n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, m: Mid { i: Inner { xs: [v] } } };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { m: Mid { i: Inner { xs: [] } }, n: 0 };\n    a = step(a, 1);\n    return a.m.i.xs.len() + __rc_underflow_count();\n}"},

	// Control: an ARRAY field in the same shape. The buffer is the box, so the
	// shallow dec was already complete — a deep walk here would be a double free,
	// not a fix.
	{"array-field-control", "struct Asm { bad: i32[], n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, bad: [v] };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { bad: [], n: 0 };\n    a = step(a, 1);\n    return a.bad.len() + __rc_underflow_count();\n}"},

	// Control: an enum field whose payload is a SCALAR. Nothing heap sits under
	// the enum box, so the shallow dec is complete and this row is clean both
	// ways — the boundary of what the missing walk costs.
	{"scalar-payload-enum-control", "enum Payload { None, Some(i32) }\nstruct Asm { p: Payload, n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, p: Payload.Some(v) };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { p: Payload.Some(0), n: 0 };\n    a = step(a, 1);\n    var r: i32 = 0;\n    match (a.p) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g; } }\n    return r + __rc_underflow_count();\n}"},

	// Control: the identical rebind with no call. It reaches the in-place reuse
	// path, which already deep-drops the replaced field — which is what said the
	// gap was in the reclaim helper rather than in the rebind.
	{"no-call-control", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\nfunction main(): i32 {\n    var a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    a = Asm { ...a, cfi: CfiState { bad: [1], open: false } };\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},
}

// TestSelfHostFieldReclaimNestedLeakCheck is the gate: clean under
// FERN_LEAKCHECK on BOTH compilers, native as the oracle.
//
// The three nested rows leak without the fix; the three controls are clean
// either way. A walk that ran unguarded, or on a field kind that did not want
// one, would show up as an over-release (exit 99) rather than a leak, which is
// why the exit code is asserted alongside the verdict.
func TestSelfHostFieldReclaimNestedLeakCheck(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldReclaimNestedCases {
		t.Run(tc.name, func(t *testing.T) {
			name := "frnleak_" + tc.name
			natV, natExit := nativeLeakVerdict(t, cli, dir, name, tc.src)
			shV, shExit := selfHostLeakVerdict(t, gcc, runner, driverBin, dir, name, tc.src)
			if natV != verdictClean {
				t.Fatalf("native is not clean on %s (%s, exit %d) — the oracle moved, re-derive before touching the self-host", tc.name, natV, natExit)
			}
			if shV != verdictClean {
				t.Errorf("self-host %s: %s (exit %d), want clean like native", tc.name, shV, shExit)
			}
			if shExit != natExit {
				t.Errorf("self-host %s exited %d under leakcheck, native %d", tc.name, shExit, natExit)
			}
		})
	}
}

// TestSelfHostFieldReclaimNestedX86_64 — the answers, against the interpreter
// oracle. The miscompile guard on the added walk, not the leak signal.
func TestSelfHostFieldReclaimNestedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldReclaimNestedCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "frn_"+tc.name, string(asm))
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

// TestSelfHostFieldReclaimNestedArm64 — the arm64 emit of the same helper. Each
// backend writes its own __field_reclaim_<T> body, so the walk lands three
// times and each needs its own frame-reload discipline: __struct_drop_<Inner>
// clobbers the registers holding new/old/snap.
func TestSelfHostFieldReclaimNestedArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldReclaimNestedCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "frn_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostFieldReclaimNestedWasmIR — the wasm leg, which needed a second
// change: its set of emitted $__struct_drop_<T> bodies is computed from the
// calls the module makes, so the reclaim body's new call had to be added to
// that closure or the module would name a function it never defines.
func TestSelfHostFieldReclaimNestedWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldReclaimNestedCases {
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
			watFile := filepath.Join(dir, "frn_"+tc.name+".wat")
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
