package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// arrFieldBuiltinRecvCases put the helper-backed i32-array builtins —
// `.sum()` / `.product()` / `.index_of(x)` / `.contains(x)` / `.min()` /
// `.max()` — on a receiver read out of a STRUCT FIELD rather than a local.
//
// Their guard used to be `expr_is_arr_src`, which reports an array SOURCE (a
// literal, an array-marked slot, an array-returning call, a slice) and has no
// field-access arm, so `h.xs.sum()` missed the intercept, fell through to the
// primitive-receiver dispatch, and emitted `call_direct "i32.sum"` — a name
// nothing declares, which `calls_only_known` then refused, bailing the whole
// module (#6784).
//
// They run under FERN_STRICT_IR=1 (#6602) because the answer alone cannot show
// the shape stayed on the IR path.
//
// The last two cases are controls that must not move: a `string[]` field was
// already covered (expr_is_strarr does have a field arm, so it reached the
// __fern_arr_str_index_of helper), and a plain array local is the path the fix
// leaves alone. An `i64[]` / `f64[]` field is deliberately NOT admitted — those
// arrays hold 8-byte elements, the helpers read 4-byte slots, so the receiver
// still falls through and the module still bails rather than summing half of
// each element.
var arrFieldBuiltinRecvCases = []struct {
	name string
	src  string
	exit int
}{
	{"field-sum", `struct H { xs: i32[] } function main(): i32 { var h: H = H { xs: [1, 2, 3] }; return h.xs.sum(); }`, 6},
	{"field-product", `struct H { xs: i32[] } function main(): i32 { var h: H = H { xs: [2, 3, 4] }; return h.xs.product(); }`, 24},
	{"field-index-of", `struct H { xs: i32[] } function main(): i32 { var h: H = H { xs: [7, 8, 9] }; return h.xs.index_of(9); }`, 2},
	{"field-index-of-missing", `struct H { xs: i32[] } function main(): i32 { var h: H = H { xs: [7, 8, 9] }; return h.xs.index_of(99) + 5; }`, 4},
	{"field-contains", `struct H { xs: i32[] } function main(): i32 { var h: H = H { xs: [7, 8, 9] }; if (h.xs.contains(8)) { return 11; } return 22; }`, 11},
	// min/max return Option[i32] from a runtime helper the module's own
	// side-tables cannot see, so the scrutinee type comes from
	// builtin_arr_opt_ret_type — which shares the lowering's receiver
	// predicate. Widening only one of the two left `match (h.xs.min())`
	// bailing on the `match` instead of on the call.
	{"field-min", `struct H { xs: i32[] } function main(): i32 { var h: H = H { xs: [5, 2, 9] }; match (h.xs.min()) { Some(m) => { return m; }, None => { return 90; } } }`, 2},
	{"field-max", `struct H { xs: i32[] } function main(): i32 { var h: H = H { xs: [5, 2, 9] }; match (h.xs.max()) { Some(m) => { return m; }, None => { return 90; } } }`, 9},
	{"field-min-empty", `struct H { xs: i32[] } function main(): i32 { var e: i32[] = []; var h: H = H { xs: e }; match (h.xs.min()) { Some(_) => { return 1; }, None => { return 42; } } }`, 42},
	// u8[] rides the same full-32-bit element slot as i32[], so a byte-buffer
	// field reduces through the same helper.
	{"u8-field-sum", `struct B { bs: u8[] } function main(): i32 { var b: B = B { bs: [1 as u8, 2 as u8, 3 as u8] }; return b.bs.sum(); }`, 6},
	{"param-field-sum", `struct H { xs: i32[] } function total(h: H): i32 { return h.xs.sum(); } function main(): i32 { var h: H = H { xs: [4, 5, 6] }; return total(h); }`, 15},
	{"nested-field-sum", `struct H { xs: i32[] } struct G { h: H } function main(): i32 { var g: G = G { h: H { xs: [1, 2, 4] } }; return g.h.xs.sum(); }`, 7},
	// The helpers BORROW the array, so reducing a field read repeatedly — via
	// a borrowed param, over a struct rebuilt each iteration — must add no rc
	// traffic at all. A stray release here would be an over-release, not a
	// wrong answer.
	{"field-reduce-no-rc-underflow", `struct H { xs: i32[] } function fold(h: H): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 20) { t = t + h.xs.sum(); i = i + 1; } return t; } function main(): i32 { var i: i32 = 0; while (i < 20) { var h: H = H { xs: [1, 2, 3] }; if (fold(h) != 120) { return 90; } i = i + 1; } return __rc_underflow_count(); }`, 0},
	{"strarr-field-index-of-unchanged", `struct S { ss: string[] } function main(): i32 { var s: S = S { ss: ["a", "b", "c"] }; return s.ss.index_of("b"); }`, 1},
	{"local-sum-unchanged", `function main(): i32 { var xs: i32[] = [1, 2, 3]; return xs.sum(); }`, 6},
}

// TestSelfHostArrFieldBuiltinRecvIRX86_64 — the x86-64 IR path.
func TestSelfHostArrFieldBuiltinRecvIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrFieldBuiltinRecvCases {
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

// TestSelfHostArrFieldBuiltinRecvIRArm64 — the arm64 IR path.
func TestSelfHostArrFieldBuiltinRecvIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range arrFieldBuiltinRecvCases {
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

// TestSelfHostArrFieldBuiltinRecvIRWasm — the wasm IR path, which is the one
// that reads an array element at the WAT-level 4-byte stride, so it is the leg
// that would notice an 8-byte-element receiver slipping into these helpers.
func TestSelfHostArrFieldBuiltinRecvIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host array-field builtin receiver wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range arrFieldBuiltinRecvCases {
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
