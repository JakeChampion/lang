package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #8604: `__field_reclaim_<T>` freed a replaced enum-ARRAY field's element boxes
// without walking their variant payloads, while its sibling `__struct_drop_<T>`
// walked them — with the walk helper emitted into the same file and simply not
// called.
//
// `field_reclaim_field_ops` built the `arrarr_free` directive's pre-walk type
// only for a struct array; there was no `is_enum_array_field_type` clause, so an
// enum array reached the backends with an empty type and got the buffer-only
// free. `struct_drop_field_ops` has had both arms — `elems_drop_struct` and
// `elems_drop_enum` — all along, which is why the same field released deeply
// when the deep drop was what reached it.
//
// The gate is `enum_arr_elems_walk_ok`, exactly the one the struct_drop side
// uses, so the two helpers now decide identically rather than by two rules that
// can drift.
//
// THE EXIT CODE DOES NOT MOVE on the leak, as with the rest of this family: the
// leakcheck leg against native is the gate. The register and wasm legs are the
// miscompile guard on adding a call inside a helper three backends emit, and the
// shared-buffer row is where an over-release would land (exit 99) rather than a
// leak.
var selfHostFieldReclaimEnumArrayCases = []struct {
	name string
	src  string
}{
	// The reported shape: an enum-array field replaced on a rebind from a call.
	{"enum-array-field-reclaim", "enum Payload { None, Some(i32[]) }\nstruct Asm { ps: Payload[], n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, ps: [Payload.Some([v])] };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { ps: [Payload.Some([9])], n: 0 };\n    a = step(a, 1);\n    return a.ps.len() + __rc_underflow_count();\n}"},

	// Two rc-carrying variants, so the walk's tag dispatch has to pick rather
	// than fall into a single arm.
	{"enum-array-two-rc-variants", "enum Payload { None, Some(i32[]), Many(i32[]) }\nstruct Asm { ps: Payload[], n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, ps: [Payload.Some([v]), Payload.Many([v, v])] };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { ps: [Payload.Some([9]), Payload.Many([8, 7])], n: 0 };\n    a = step(a, 1);\n    return a.ps.len() + __rc_underflow_count();\n}"},

	// Several rounds, so a per-round leak accumulates rather than resting on one
	// missed walk.
	{"enum-array-three-rounds", "enum Payload { None, Some(i32[]) }\nstruct Asm { ps: Payload[], n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, ps: [Payload.Some([v])] };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { ps: [Payload.Some([9])], n: 0 };\n    a = step(a, 1);\n    a = step(a, 2);\n    a = step(a, 3);\n    return a.ps.len() + __rc_underflow_count();\n}"},

	// The SAME field one level down, so `__struct_drop_Inner` is what releases
	// it. It was already clean; keeping it here is what says the two helpers
	// agree now rather than that the reclaim side merely stopped leaking.
	{"enum-array-via-struct-drop", "enum Payload { None, Some(i32[]) }\nstruct Inner { ps: Payload[] }\nstruct Asm { i: Inner, n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, i: Inner { ps: [Payload.Some([v])] } };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { i: Inner { ps: [Payload.Some([9])] }, n: 0 };\n    a = step(a, 1);\n    return a.i.ps.len() + __rc_underflow_count();\n}"},

	// A SECOND owner holds the buffer across the rebind, so the walk's rc==1
	// gate must decline it — that owner still reads the payload afterwards. An
	// unguarded walk fails this row as a changed answer or an underflow, not as
	// a leak. The second owner is a struct on purpose: an enum-array LOCAL is
	// released by a different path, which has a leak of its own (#8610) that
	// would confound this row.
	{"shared-via-second-struct-declines-walk", "enum Payload { None, Some(i32[]) }\nstruct Asm { ps: Payload[], n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, ps: [Payload.Some([v])] };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { ps: [Payload.Some([9, 9, 9])], n: 0 };\n    var b: Asm = Asm { ps: a.ps, n: 1 };\n    a = step(a, 1);\n    var r: i32 = 0;\n    match (b.ps[0]) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g.len(); } }\n    return r + __rc_underflow_count();\n}"},

	// Control: an enum array whose payloads are all SCALAR. enum_arr_elems_walk_ok
	// requires a rc payload, so this must NOT gain a walk — nothing heap sits
	// under those element boxes and a dec there would be an over-release.
	{"scalar-payload-enum-array-control", "enum Payload { None, Some(i32) }\nstruct Asm { ps: Payload[], n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, ps: [Payload.Some(v)] };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { ps: [Payload.Some(0)], n: 0 };\n    a = step(a, 1);\n    return a.ps.len() + __rc_underflow_count();\n}"},

	// Control: the STRUCT-array sibling in the same helper, which already had its
	// pre-walk. Clean either way — it says the arrarr_free mechanism was sound
	// and the enum arm was what was missing.
	{"struct-array-field-reclaim-control", "struct Slot { xs: i32[] }\nstruct Asm { cs: Slot[], n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, cs: [Slot { xs: [v] }] };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { cs: [Slot { xs: [9] }], n: 0 };\n    a = step(a, 1);\n    return a.cs.len() + __rc_underflow_count();\n}"},
}

// TestSelfHostFieldReclaimEnumArrayLeakCheck is the gate: clean under
// FERN_LEAKCHECK on BOTH compilers, native as the oracle.
//
// The first three rows leak without the fix; the four controls are clean either
// way. An unguarded or wrongly-typed walk shows up as an over-release rather
// than a leak, which is why the exit code is asserted alongside the verdict.
func TestSelfHostFieldReclaimEnumArrayLeakCheck(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldReclaimEnumArrayCases {
		t.Run(tc.name, func(t *testing.T) {
			name := "frealeak_" + tc.name
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

// TestSelfHostFieldReclaimEnumArrayX86_64 — the answers against the interpreter
// oracle. Vacuous for the leak itself; it is the miscompile guard on adding a
// call inside the reclaim helper, and where the shared-buffer row's over-release
// would surface.
func TestSelfHostFieldReclaimEnumArrayX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldReclaimEnumArrayCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "frea_"+tc.name, string(asm))
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

// TestSelfHostFieldReclaimEnumArrayArm64 — the arm64 emit of the same directive.
func TestSelfHostFieldReclaimEnumArrayArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldReclaimEnumArrayCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "frea_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostFieldReclaimEnumArrayWasmIR — wasm builds its reclaim body itself
// rather than from the shared directives, so it carried the identical omission
// and needs its own proof.
func TestSelfHostFieldReclaimEnumArrayWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range selfHostFieldReclaimEnumArrayCases {
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
			watFile := filepath.Join(dir, "frea_"+tc.name+".wat")
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
