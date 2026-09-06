package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #8567: a DIRECT enum field with an rc payload was released box-only, stranding
// the live variant's payloads. `fr_enum` in field_reclaim_field_ops fell through
// its arm chain to the shallow `arr_dec`, with no walk to reach for:
// `__enum_arr_elems_drop_<E>` is a variant walk and all three backends emit it,
// but it takes a BUFFER and loops. What was missing was the single-box step.
//
// So the per-element body inside that loop is its own helper,
// `__enum_drop_<E>(box)`, on all three backends, and the array walk delegates to
// it — a direct enum field and an enum-array element release through the same
// code rather than through two that can drift.
//
// ONLY the field-reclaim side takes it. The sibling `k_enum` arm in
// struct_drop_field_ops must NOT: an enum field has a second releaser, the inline
// emit_struct_enum_field_payload_drops sweep at a struct local's scope exit, and
// `__struct_drop_` cannot tell whether its caller already ran it. Both are
// rc==1-gated, so in a scope-exit context both fire and the second dec lands past
// a live claim. The first cut of this fix did walk there and took
// TestSelfHostEnumFieldShare and TestSelfHostStructEnumFieldPayloadDrop to exit 99
// on all three backends. Unifying the two releasers is #8692; the
// enum-field-via-struct-drop row below is that gap, pinned as a leak.
//
// THE EXIT CODE DOES NOT MOVE on the leak: the leakcheck leg against native is
// the gate. The register and wasm legs are the miscompile guard on a new helper
// three backends emit, and where an over-release would land as exit 99.
var selfHostEnumFieldDeepDropCases = []struct {
	name string
	src  string
	// refused: outside the fix on purpose, still taking the leak-safe shallow
	// path. The leakcheck leg asserts the row stays SAFE rather than clean.
	refused bool
}{
	// Route 1: the field reclaim releases the replaced enum field.
	{"enum-field-via-field-reclaim", "enum Payload { None, Some(i32[]) }\nstruct Asm { p: Payload, n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, p: Payload.Some([v]) };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { p: Payload.Some([9]), n: 0 };\n    a = step(a, 1);\n    var r: i32 = 0;\n    match (a.p) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g.len(); } }\n    return r + __rc_underflow_count();\n}", false},

	// Route 2, and NOT fixed — pinned here rather than dropped. Reached only
	// through `__struct_drop_Inner`, which must NOT walk an enum field: the
	// inline emit_struct_enum_field_payload_drops sweep is that field's other
	// releaser, both are rc==1-gated, and in a scope-exit context both fire, so
	// the second dec lands past a live claim. #8567's first cut did walk here and
	// took TestSelfHostEnumFieldShare and TestSelfHostStructEnumFieldPayloadDrop
	// to exit 99 on all three backends. Unifying the two releasers is #8692; until
	// then this row leaks, which is the sound side.
	{"enum-field-via-struct-drop", "enum Payload { None, Some(i32[]) }\nstruct Inner { p: Payload }\nstruct Asm { i: Inner, n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, i: Inner { p: Payload.Some([v]) } };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { i: Inner { p: Payload.Some([9]) }, n: 0 };\n    a = step(a, 1);\n    var r: i32 = 0;\n    match (a.i.p) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g.len(); } }\n    return r + __rc_underflow_count();\n}", true},

	// Two rc-carrying variants, so the tag dispatch has to pick rather than fall
	// into a single arm.
	{"two-rc-variants", "enum Payload { None, Some(i32[]), Many(i32[]) }\nstruct Asm { p: Payload, n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, p: Payload.Many([v, v]) };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { p: Payload.Some([9]), n: 0 };\n    a = step(a, 1);\n    var r: i32 = 0;\n    match (a.p) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g.len(); }, Payload.Many(h) => { r = h.len(); } }\n    return r + __rc_underflow_count();\n}", false},

	// Three rounds, so a per-round leak accumulates rather than resting on one
	// missed release.
	{"three-rounds", "enum Payload { None, Some(i32[]) }\nstruct Asm { p: Payload, n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, p: Payload.Some([v]) };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { p: Payload.Some([9]), n: 0 };\n    a = step(a, 1);\n    a = step(a, 2);\n    a = step(a, 3);\n    var r: i32 = 0;\n    match (a.p) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g.len(); } }\n    return r + __rc_underflow_count();\n}", false},

	// An enum LOCAL aliases the field across the supersede, so __enum_drop_'s
	// rc==1 gate must decline the box — the local still reads the payload
	// afterwards. This is the direct guard on that gate: without it the walk
	// frees a payload a live name still reaches, which lands as a changed answer
	// or an underflow, not as a leak.
	{"local-alias-declines-walk", "enum Payload { None, Some(i32[]) }\nstruct Asm { p: Payload, n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, p: Payload.Some([v]) };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { p: Payload.Some([9, 9, 9]), n: 0 };\n    var keep: Payload = a.p;\n    a = step(a, 1);\n    var r: i32 = 0;\n    match (keep) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g.len(); } }\n    return r + __rc_underflow_count();\n}", false},

	// The enum box is sole-owned but its PAYLOAD buffer is shared with a local, so
	// the walk does run and its dec must be balanced by the construction's retain.
	// The other half of the guard: the row above proves the gate declines a shared
	// box, this one proves the dec is right when it does not.
	{"shared-payload-buffer", "enum Payload { None, Some(i32[]) }\nstruct Asm { p: Payload, n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, p: Payload.Some([v]) };\n}\nfunction main(): i32 {\n    var buf: i32[] = [9, 9, 9];\n    var a: Asm = Asm { p: Payload.Some(buf), n: 0 };\n    a = step(a, 1);\n    return buf.len() + __rc_underflow_count();\n}", false},

	// Control: a SCALAR payload. enum_arr_elems_walk_ok requires an rc payload, so
	// this must NOT gain a walk — nothing heap sits under the box and a dec there
	// would be an over-release. Clean before and after.
	{"scalar-payload-control", "enum Payload { None, Some(i32) }\nstruct Asm { p: Payload, n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, p: Payload.Some(v) };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { p: Payload.Some(0), n: 0 };\n    a = step(a, 1);\n    var r: i32 = 0;\n    match (a.p) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g; } }\n    return r + __rc_underflow_count();\n}", false},

	// Control: the same enum as an ARRAY field, which #8604 fixed and whose walk
	// now delegates to the new helper. Clean before and after — the refactor must
	// not have moved it.
	{"enum-array-field-control", "enum Payload { None, Some(i32[]) }\nstruct Asm { ps: Payload[], n: i32 }\n@noinline\nfunction step(a: Asm, v: i32): Asm {\n    return Asm { ...a, ps: [Payload.Some([v])] };\n}\nfunction main(): i32 {\n    var a: Asm = Asm { ps: [Payload.Some([9])], n: 0 };\n    a = step(a, 1);\n    return a.ps.len() + __rc_underflow_count();\n}", false},
}

// TestSelfHostEnumFieldDeepDropLeakCheck is the gate: clean under FERN_LEAKCHECK
// on BOTH compilers, native as the oracle.
//
// FIVE rows leak without the fix: the three defect rows and both guard rows. The
// guards earn their name from the other direction — they are the rows an UNGATED
// or unbalanced walk would fail, and it would fail them as a changed answer or an
// underflow rather than as a leak, which is why the exit code is asserted
// alongside the verdict. The two controls are clean either way, and the refused
// row leaks either way.
//
// The obvious guard shape — a SECOND STRUCT holding the enum box across the
// supersede — is deliberately not here: it leaks 88 B/round for an unrelated
// reason (#8658, measured with and without this fix, and it moves for neither),
// which would confound the row. The two guards below reach the same gate without
// it.
func TestSelfHostEnumFieldDeepDropLeakCheck(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostEnumFieldDeepDropCases {
		t.Run(tc.name, func(t *testing.T) {
			name := "efdd_" + tc.name
			natV, natExit := nativeLeakVerdict(t, cli, dir, name, tc.src)
			shV, shExit := selfHostLeakVerdict(t, gcc, runner, driverBin, dir, name, tc.src)
			if natV != verdictClean {
				t.Fatalf("native is not clean on %s (%s, exit %d) — the oracle moved, re-derive before touching the self-host", tc.name, natV, natExit)
			}
			if tc.refused {
				// The boundary row (#8692): a leak is the sound refusal here. A
				// CRASH or a changed answer would mean a release ran anyway.
				if shV == verdictCrash {
					t.Errorf("refused row %s crashed (exit %d) — the shallow path must stay safe", tc.name, shExit)
				}
				if shExit != natExit {
					t.Errorf("refused row %s exited %d, native %d — the answer moved, so the field was released under a live claim", tc.name, shExit, natExit)
				}
				return
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

// TestSelfHostEnumFieldDeepDropX86_64 — the answers against the interpreter
// oracle, and where the shared-box row's over-release would surface.
func TestSelfHostEnumFieldDeepDropX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostEnumFieldDeepDropCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "efdd_"+tc.name, string(asm))
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

// TestSelfHostEnumFieldDeepDropArm64 — the arm64 emit of the new helper.
func TestSelfHostEnumFieldDeepDropArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostEnumFieldDeepDropCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "efdd_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostEnumFieldDeepDropWasmIR — wasm emits its own helper body and its
// own reclaim, so both call sites need proving there too.
func TestSelfHostEnumFieldDeepDropWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range selfHostEnumFieldDeepDropCases {
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
			watFile := filepath.Join(dir, "efdd_"+tc.name+".wat")
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
