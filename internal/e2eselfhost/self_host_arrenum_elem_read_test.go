package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #8610: an enum-array LOCAL was released only when nothing read an element, so
// `match (xs[i])` cost the whole structure — every element box, and any rc
// payload under it, per construction.
//
// The walk was never the problem. `slot_is_reclaimable_arrenum` simply refused
// the credit: `arrenum_esc_expr` admitted `xs.len()` and nothing else, so an
// element read fell back to the shallow buffer dec and no element release was
// emitted at all. The same local with only `.len()` read has always been clean,
// which is the measurement that located it.
//
// The widening is the follow-up `arrenum_esc_expr`'s own comment named. A
// `match (xs[i])` scrutinee is now vetted at the STATEMENT level, where the arms
// are visible: the credit survives when every arm confines its bindings to the
// arm. That is sound because the reclaim runs AFTER the whole match, so a binding
// whose last use is inside its arm is already dead when the element is freed.
//
// Two readings make a binding confined. A POINTER payload must be borrow-only
// (binding_escapes_arm, the gate the enum path already applies to its own arm
// bindings). A SCALAR payload is a value copy that aliases nothing, so it is
// confined however the arm uses it — the same reading arrtup gives a scalar tuple
// element, and without it `Some(n) => { total = n; }` would be read as an escape.
//
// The hazard being guarded is a DOUBLE FREE, not a leak: freeing an element while
// a binding still names its payload corrupts the self-compile. So the exit codes
// here are load-bearing in a way this family's usually are not — the register and
// wasm legs check every row against the interpreter, and an over-admitted rule
// shows up there as a wrong answer or an underflow rather than as a leak.
var selfHostArrEnumElemReadCases = []struct {
	name string
	src  string
	// refused: this row is OUTSIDE the widening on purpose and still takes the
	// leak-safe shallow path. The leakcheck leg asserts it does not CRASH or
	// over-release rather than asserting it is clean — pinning the boundary
	// instead of pretending it moved.
	refused bool
}{
	// The reported shape: rc payload, borrow-only arm binding.
	{"rc-payload-borrow-read", "enum Payload { None, Some(i32[]) }\nfunction main(): i32 {\n    var keep: Payload[] = [Payload.Some([9, 9, 9])];\n    var r: i32 = 0;\n    match (keep[0]) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g.len(); } }\n    return r + __rc_underflow_count();\n}", false},

	// A SCALAR payload stored straight out to an outer local. Not an escape — it
	// is a copy — and this row is what says the rule reads it that way.
	{"scalar-payload-stored-out", "enum Payload { None, Some(i32) }\nfunction main(): i32 {\n    var keep: Payload[] = [Payload.Some(3)];\n    var r: i32 = 0;\n    match (keep[0]) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g; } }\n    return r + __rc_underflow_count();\n}", false},

	// In a loop, so a per-round leak accumulates rather than resting on one missed
	// release. This is the unbounded form the issue measured at 88 B/round.
	{"loop-thirty-rounds", "enum Payload { None, Some(i32[]) }\nfunction main(): i32 {\n    var r: i32 = 0;\n    var i: i32 = 0;\n    while (i < 30) {\n        var keep: Payload[] = [Payload.Some([9, 9, 9])];\n        match (keep[0]) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g.len(); } }\n        i = i + 1;\n    }\n    return r + __rc_underflow_count();\n}", false},

	// Two rc-carrying variants, so the arm vet has to clear both rather than the
	// one the scrutinee happens to hold.
	{"two-rc-variants-both-borrowed", "enum Payload { None, Some(i32[]), Many(i32[]) }\nfunction main(): i32 {\n    var keep: Payload[] = [Payload.Many([7, 7])];\n    var r: i32 = 0;\n    match (keep[0]) { Payload.None => { r = 0; }, Payload.Some(g) => { r = g.len(); }, Payload.Many(h) => { r = h.len(); } }\n    return r + __rc_underflow_count();\n}", false},

	// Control: `.len()` only, which the old rule already admitted. Clean before
	// and after — it says the widening did not disturb the admitted shape.
	{"len-only-control", "enum Payload { None, Some(i32[]) }\nfunction main(): i32 {\n    var keep: Payload[] = [Payload.Some([9, 9, 9])];\n    return keep.len() + __rc_underflow_count();\n}", false},

	// THE BOUNDARY, and deliberately still refused: the arm binds a POINTER
	// payload and stores it to an outer local, so it outlives the arm. Admitting
	// this row would free a buffer `out` still names. It keeps the leak-safe
	// shallow path, and the assertion below is that it stays SAFE — right answer,
	// no underflow — not that it stopped leaking.
	{"escaping-pointer-binding-stays-refused", "enum Payload { None, Some(i32[]) }\nfunction main(): i32 {\n    var out: i32[] = [];\n    var keep: Payload[] = [Payload.Some([9, 9, 9])];\n    match (keep[0]) { Payload.None => { }, Payload.Some(g) => { out = g; } }\n    return out.len() + __rc_underflow_count();\n}", true},
}

// TestSelfHostArrEnumElemReadLeakCheck is the gate. Every admitted row must be
// clean under FERN_LEAKCHECK on both compilers with native as the oracle; the
// refused row must merely stay SAFE, which is what its boundary status means.
//
// Without the widening the four admitted read rows leak and the two controls do
// not move.
func TestSelfHostArrEnumElemReadLeakCheck(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostArrEnumElemReadCases {
		t.Run(tc.name, func(t *testing.T) {
			name := "aer_" + tc.name
			natV, natExit := nativeLeakVerdict(t, cli, dir, name, tc.src)
			shV, shExit := selfHostLeakVerdict(t, gcc, runner, driverBin, dir, name, tc.src)
			if natV != verdictClean {
				t.Fatalf("native is not clean on %s (%s, exit %d) — the oracle moved, re-derive before touching the self-host", tc.name, natV, natExit)
			}
			if tc.refused {
				// The boundary row: a leak is the sound refusal. A CRASH or a
				// changed answer would mean the rule admitted it after all.
				if shV == verdictCrash {
					t.Errorf("refused row %s crashed (exit %d) — the shallow path must stay safe", tc.name, shExit)
				}
				if shExit != natExit {
					t.Errorf("refused row %s exited %d, native %d — the answer moved, so the element was released under a live alias", tc.name, shExit, natExit)
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

// TestSelfHostArrEnumElemReadX86_64 — every row against the interpreter oracle.
// Load-bearing here: an over-admitted arm vet frees an element under a live
// binding, and that lands as a wrong answer or an underflow on this leg.
func TestSelfHostArrEnumElemReadX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostArrEnumElemReadCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "aer_"+tc.name, string(asm))
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

// TestSelfHostArrEnumElemReadArm64 — the credit is shared irlower analysis, so
// this leg is where an added release landing on one register backend would show.
func TestSelfHostArrEnumElemReadArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostArrEnumElemReadCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "aer_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostArrEnumElemReadWasmIR — wasm emits its own element release from the
// same credit, so the widening needs proving there too.
func TestSelfHostArrEnumElemReadWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range selfHostArrEnumElemReadCases {
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
			watFile := filepath.Join(dir, "aer_"+tc.name+".wat")
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
