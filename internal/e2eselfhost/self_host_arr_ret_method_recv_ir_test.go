package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// arrRetMethodRecvCases consume an array-returning user METHOD's result in
// expression position — `h.get().len()`, `h.get()[i].field`, `for x in
// h.get()` — without binding it to a local first.
//
// `expr_is_arr_src` had no arm for such a call. Its free-function limb reads
// arr_ret_fns, and its field-access limb knew only the builtins (`.bytes()`,
// `.split()`, `.keys()`) and the std/array helpers, so a plain user method fell
// through. That left the `.len()` dispatch gate resting on its last disjunct,
// "the receiver is not a struct" — and `expr_struct_type` reports a
// `P[]`-returning call as the ELEMENT type P, because the struct_ret_fns
// registry strips the `[]`. So for a STRUCT-element array the fallback denied
// an array it could not otherwise see and `.len()` resolved against P, emitting
// `Inner.len`: a symbol nothing declares (#7627).
//
// The sibling element kinds escaped only by accident — `string[]`, `i32[]` and
// `E[]` method results report "" from expr_struct_type, so the same fallback
// admitted them. They are controls here: the fix must not move them.
//
// A struct-array element indexed straight off the method result
// (`h.get()[0].k`) needs the matching half in expr_struct_type's ExprIndex arm,
// which recovered a free-fn callee's element type but not a method's.
//
// They run under FERN_STRICT_IR=1 (#6602) because the answer alone cannot show
// the shape stayed on the IR path: a per-function bail reaches the same exit
// code by another route, so these would pass unfixed without the flag.
var arrRetMethodRecvCases = []struct {
	name string
	src  string
	exit int
}{
	// The #7627 repro: the shape that emitted `Inner.len`.
	{"struct-arr-method-len", structArrPrelude + `function main(): i32 { var h: Holder = mkh(); return h.get().len(); }`, 2},
	{"struct-arr-method-index-field", structArrPrelude + `function main(): i32 { var h: Holder = mkh(); return h.get()[0].k; }`, 3},
	{"struct-arr-method-index-nested-len", structArrPrelude + `function main(): i32 { var h: Holder = mkh(); return h.get()[1].ys.len(); }`, 1},
	{"struct-arr-method-foreach", structArrPrelude + `function main(): i32 { var h: Holder = mkh(); var t: i32 = 0; for x in h.get() { t = t + x.k; } return t; }`, 7},
	// Binding to an annotated local first was the documented workaround, and
	// it went down a different path — it must keep working.
	{"struct-arr-method-bound-first", structArrPrelude + `function main(): i32 { var h: Holder = mkh(); var tmp: Inner[] = h.get(); return tmp.len(); }`, 2},

	// Sibling element kinds: clean before the fix, and still clean after.
	{"enum-arr-method-len", `enum Col { R, G } struct EH { es: Col[] } function (h: EH) get(): Col[] { return h.es; } function main(): i32 { var h: EH = EH { es: [Col.R, Col.G] }; return h.get().len(); }`, 2},
	{"strarr-method-len", strArrPrelude + `function main(): i32 { var h: SH = SH { ss: ["a", "b", "c"] }; return h.get().len(); }`, 3},
	{"strarr-method-elem-len", strArrPrelude + `function main(): i32 { var h: SH = SH { ss: ["abcd", "b"] }; return h.get()[0].len(); }`, 4},
	{"i32arr-method-len", i32ArrPrelude + `function main(): i32 { var h: IH = IH { xs: [7, 8] }; return h.get().len(); }`, 2},
	{"i32arr-method-index", i32ArrPrelude + `function main(): i32 { var h: IH = IH { xs: [7, 8] }; return h.get()[1]; }`, 8},

	// The free-FUNCTION limb is the one that always worked (arr_ret_fns keyed
	// by bare name); it is the control the method limb was modelled on.
	{"free-fn-arr-ret-len", freeFnArrPrelude + `function main(): i32 { return mk().len(); }`, 2},
	{"free-fn-arr-ret-index-field", freeFnArrPrelude + `function main(): i32 { return mk()[1].k; }`, 4},

	// expr_is_arr_src drives RC decisions, so admitting a new expression to it
	// risks an over-release rather than a wrong answer. Each of these calls the
	// method repeatedly against a struct rebuilt every round; a stray release
	// shows up as an underflow, not as a bad exit code. (The unbound result
	// still strands its retain — that is #7259, a leak on this same shape and
	// on the free-fn limb alike, and it is not what these pin.)
	{"struct-arr-loop-no-underflow", structArrPrelude + `function main(): i32 { var i: i32 = 0; while (i < 30) { var h: Holder = mkh(); if (h.get().len() != 2) { return 90; } i = i + 1; } return __rc_underflow_count(); }`, 0},
	{"struct-arr-index-loop-no-underflow", structArrPrelude + `function main(): i32 { var i: i32 = 0; while (i < 30) { var h: Holder = mkh(); if (h.get()[1].k != 4) { return 90; } i = i + 1; } return __rc_underflow_count(); }`, 0},
	{"struct-arr-foreach-loop-no-underflow", structArrPrelude + `function main(): i32 { var i: i32 = 0; while (i < 30) { var h: Holder = mkh(); var t: i32 = 0; for x in h.get() { t = t + x.k; } if (t != 7) { return 90; } i = i + 1; } return __rc_underflow_count(); }`, 0},
	{"strarr-loop-no-underflow", strArrPrelude + `function main(): i32 { var i: i32 = 0; while (i < 30) { var h: SH = SH { ss: ["a", "b"] }; if (h.get().len() != 2) { return 90; } i = i + 1; } return __rc_underflow_count(); }`, 0},
}

const (
	structArrPrelude = `struct Inner { k: i32, ys: i32[] } struct Holder { xs: Inner[] } ` +
		`function (h: Holder) get(): Inner[] { return h.xs; } ` +
		`function mkh(): Holder { return Holder { xs: [Inner { k: 3, ys: [1, 2] }, Inner { k: 4, ys: [5] }] }; } `
	strArrPrelude    = `struct SH { ss: string[] } function (h: SH) get(): string[] { return h.ss; } `
	i32ArrPrelude    = `struct IH { xs: i32[] } function (h: IH) get(): i32[] { return h.xs; } `
	freeFnArrPrelude = `struct Inner2 { k: i32 } function mk(): Inner2[] { return [Inner2 { k: 3 }, Inner2 { k: 4 }]; } `
)

// TestSelfHostArrRetMethodRecvIRX86_64 — the x86-64 IR path.
func TestSelfHostArrRetMethodRecvIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrRetMethodRecvCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostArrRetMethodRecvIRArm64 — the arm64 IR path.
func TestSelfHostArrRetMethodRecvIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrRetMethodRecvCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostArrRetMethodRecvIRWasm — the wasm IR path. Its arrays carry no rc
// header, so it is the leg where a misplaced rc touch corrupts a neighbouring
// allocation rather than a refcount.
func TestSelfHostArrRetMethodRecvIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host array-returning-method receiver wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range arrRetMethodRecvCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
