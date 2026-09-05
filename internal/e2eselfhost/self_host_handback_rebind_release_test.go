package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #8527: `x = f(…, x, …)` where f hands its parameter back emitted NO exit
// release for x at all.
//
// A callee that returns its parameter gives the box back with no counted
// reference (the convention #8240 settled: a returned param is an UNCOUNTED
// alias). reclaimable_fresh_struct therefore refused the reclaim credit to any
// name passed at such a position, because a SECOND name for that box would then
// run a second release.
//
// A self-rebind makes no second name. One slot, one claim across the call, so
// the exit sweep owes exactly one release — and withholding it strands the whole
// value: zero `__struct_drop_<T>`, zero box dec in the caller.
//
// THE EXIT CODE DOES NOT MOVE. A leak reads the same answer and never trips
// __rc_underflow_count(), so the register legs below are here to catch a
// miscompile from the change, not the defect: the LEAKCHECK leg is the gate,
// and it takes native as the oracle rather than a written-down verdict.
//
// Every case uses a plain array field. A nested-struct or rc-payload-enum field
// leaks through a different hole (#8538, the shallow field release), which would
// confound these rows.
var selfHostHandbackRebindCases = []struct {
	name string
	src  string
}{
	// The reported shape, at its smallest: an `own` callee that returns its
	// parameter unchanged.
	{"own-handback-self-rebind", "struct Asm { bad: i32[], n: i32 }\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    return a;\n}\nfunction main(): i32 {\n    var a: Asm = Asm { bad: [1], n: 0 };\n    a = step(a, 1);\n    return a.bad.len() + __rc_underflow_count();\n}"},

	// The same with the callee superseding a field first. The supersede was in
	// every row of the original #8527 report and is irrelevant to the defect —
	// this row is what says so.
	{"own-handback-supersede", "struct Asm { bad: i32[], n: i32 }\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    a = Asm { ...a, bad: [v] };\n    return a;\n}\nfunction main(): i32 {\n    var a: Asm = Asm { bad: [], n: 0 };\n    a = step(a, 1);\n    return a.bad.len() + __rc_underflow_count();\n}"},

	// Two rounds, so a per-round leak accumulates rather than resting on one
	// missed release.
	{"own-handback-twice", "struct Asm { bad: i32[], n: i32 }\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    a = Asm { ...a, bad: [v] };\n    return a;\n}\nfunction main(): i32 {\n    var a: Asm = Asm { bad: [], n: 0 };\n    a = step(a, 1);\n    a = step(a, 2);\n    return a.bad.len() + __rc_underflow_count();\n}"},

	// A BORROWED parameter handed back. The caller never moved its claim, so it
	// still holds exactly one and still owes one release — the same arithmetic,
	// reached without `own`.
	{"borrowed-handback-self-rebind", "struct Asm { bad: i32[], n: i32 }\n@noinline\nfunction keepit(a: Asm, v: i32): Asm {\n    return a;\n}\nfunction main(): i32 {\n    var a: Asm = Asm { bad: [1], n: 0 };\n    a = keepit(a, 1);\n    return a.bad.len() + __rc_underflow_count();\n}"},

	// Control: the callee returns a FRESH box instead of its parameter, so no
	// handback is involved and the credit was never in question. Clean before
	// and after.
	{"fresh-return-control", "struct Asm { bad: i32[], n: i32 }\n@noinline\nfunction step(own a: Asm, v: i32): Asm {\n    return Asm { bad: [v], n: a.n };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { bad: [], n: 0 };\n    a = step(a, 1);\n    return a.bad.len() + __rc_underflow_count();\n}"},

	// Control: no call at all. The in-body rebind reclaims correctly, which is
	// what says the defect is the handback rather than the rebind.
	{"no-call-control", "struct Asm { bad: i32[], n: i32 }\nfunction main(): i32 {\n    var a: Asm = Asm { bad: [], n: 0 };\n    a = Asm { ...a, bad: [1] };\n    a = Asm { ...a, bad: [1, 2] };\n    return a.bad.len() + __rc_underflow_count();\n}"},
}

// TestSelfHostHandbackRebindLeakCheck is the gate: every case clean under
// FERN_LEAKCHECK on BOTH compilers, with native as the oracle.
//
// The four handback rows all leak without the fix — measured on `main`, where
// the caller emits no release for the slot at all — while the two controls are
// clean either way. A rule that granted the credit unconditionally would show up
// here as an over-release on some other row, not as a leak, which is why the
// exit code is asserted alongside the verdict.
func TestSelfHostHandbackRebindLeakCheck(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostHandbackRebindCases {
		t.Run(tc.name, func(t *testing.T) {
			name := "hbleak_" + tc.name
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

// TestSelfHostHandbackRebindX86_64 — the answers, against the interpreter
// oracle. Vacuous for the leak itself; it is the miscompile guard on a change
// that adds a release where there was none.
func TestSelfHostHandbackRebindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostHandbackRebindCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "hb_"+tc.name, string(asm))
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

// TestSelfHostHandbackRebindArm64 — the credit is shared irlower analysis, so
// this leg is what would catch the added release landing on one register
// backend.
func TestSelfHostHandbackRebindArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostHandbackRebindCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "hb_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostHandbackRebindWasmIR — the wasm leg emits its own exit sweep, so
// a credit granted in shared analysis needs proving there too.
func TestSelfHostHandbackRebindWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range selfHostHandbackRebindCases {
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
			watFile := filepath.Join(dir, "hb_"+tc.name+".wat")
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
