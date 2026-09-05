package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #8267: a field lifted into a local and then handed to an `own` parameter was
// freed twice.
//
// `var st = a.cfi` emits no retain — the local is an UNCOUNTED alias and the
// base's box keeps the only claim. Passing it to a declared `own` parameter
// gives the callee a claim that was never made: the callee releases it at exit
// (or, having found it unique, reuses the box in place and hands it back), and
// `a`'s own deep drop releases it again. When the returned box IS that field,
// the result is built on freed memory.
//
// A field read passed DIRECTLY to an `own` position already takes a transfer
// retain (emit_own_field_arg, #8186). The fix is that same pairing one binding
// along, so the two release paths have two claims between them.
//
// Assertions are on the answer AND __rc_underflow_count(): this is a genuine
// over-release, so the counter moves, unlike the #8198 family where the free is
// at rc 1 and only the value dissents. The controls are the leak direction — a
// retain emitted where the local already owns its value strands the box, which
// no exit code reports, so they are also run under FERN_LEAKCHECK by
// TestSelfHostOwnParamLiftedFieldLeakCheck below.
var selfHostOwnParamLiftedFieldCases = []struct {
	name string
	src  string
}{
	// The reported shape: lift a field of the `own` param, hand it to a callee
	// that consumes it, return it inside a box built from the result.
	{"lifted-field-returned", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(own s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    var st: CfiState = a.cfi;\n    st = directive(st, v);\n    return Asm { cfi: st, n: a.n };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    a = step(a, 1);\n    a = step(a, 2);\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	// The same with the write-back spelled as a spread. Both spellings reach the
	// same emit today; the analysis reads the SOURCE, so pinning both is what
	// says the fix is at the argument rather than in one literal form.
	{"lifted-field-returned-spread", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(own s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    var st: CfiState = a.cfi;\n    st = directive(st, v);\n    return Asm { ...a, cfi: st };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    a = step(a, 1);\n    a = step(a, 2);\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	// Control: the callee BORROWS the field, so nothing consumes the alias and
	// the base's single claim is still the right count. Correct before the fix,
	// and the row that fails if the retain is hung on the LIFT instead of on the
	// `own` position — there it strands the field's box.
	{"borrowed-callee-control", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    var st: CfiState = a.cfi;\n    st = directive(st, v);\n    return Asm { cfi: st, n: a.n };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    a = step(a, 1);\n    a = step(a, 2);\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	// Control: nothing is lifted — `st` is built fresh and owns its claim, so
	// the `own` position takes it as it stands. A retain here would leak.
	{"no-lift-control", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(own s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    var st: CfiState = CfiState { bad: [v], open: true };\n    st = directive(st, v);\n    return Asm { cfi: st, n: a.n };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    a = step(a, 1);\n    a = step(a, 2);\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},

	// Control: the local IS lifted, but is rebound to a fresh box before the
	// `own` call, so by then it owns its value and the alias is gone. This is
	// what the forward scan's stop-on-rebind is for; without it the retain lands
	// on a counted value and strands it.
	{"lift-then-rebind-control", "struct CfiState { bad: i32[], open: boolean }\nstruct Asm { cfi: CfiState, n: i32 }\n@noinline\nfunction directive(own s: CfiState, v: i32): CfiState {\n    return CfiState { ...s, bad: s.bad.append(v), open: true };\n}\n@noinline\nfunction fresh(v: i32): CfiState {\n    return CfiState { bad: [v], open: false };\n}\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    var st: CfiState = a.cfi;\n    st = fresh(v);\n    st = directive(st, v);\n    return Asm { cfi: st, n: a.n };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { cfi: CfiState { bad: [], open: false }, n: 0 };\n    a = step(a, 1);\n    a = step(a, 2);\n    return a.cfi.bad.len() + __rc_underflow_count();\n}"},
}

// TestSelfHostOwnParamLiftedFieldX86_64 — the production x86-64 IR path against
// the interpreter oracle. The two lifted rows exit 3 without the fix: 2 for the
// answer plus the over-release the counter reports.
func TestSelfHostOwnParamLiftedFieldX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostOwnParamLiftedFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "oplf_"+tc.name, string(asm))
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

// TestSelfHostOwnParamLiftedFieldArm64 — the same cases through the arm64 emit.
// The retain is inserted by shared irlower analysis rather than per-backend
// emission, so this leg is what would catch it landing on one register backend.
func TestSelfHostOwnParamLiftedFieldArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostOwnParamLiftedFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "oplf_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostOwnParamLiftedFieldWasmIR — the wasm leg, which emits its own
// call-argument sequence and exit sweep.
func TestSelfHostOwnParamLiftedFieldWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range selfHostOwnParamLiftedFieldCases {
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
			watFile := filepath.Join(dir, "oplf_"+tc.name+".wat")
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

// TestSelfHostOwnParamLiftedFieldLeakCheck runs every case under
// FERN_LEAKCHECK, on BOTH compilers, and requires clean on each.
//
// The exit-code legs above only see the over-release direction. An
// over-retained alias — the failure mode of hanging the retain on the lift
// rather than on the `own` position, or of scanning past the rebind — leaks
// instead: same answer, same counter, live bytes at exit. Native is the oracle
// here rather than a written-down verdict, and it is clean on every row.
func TestSelfHostOwnParamLiftedFieldLeakCheck(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostOwnParamLiftedFieldCases {
		t.Run(tc.name, func(t *testing.T) {
			name := "oplfleak_" + tc.name
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
