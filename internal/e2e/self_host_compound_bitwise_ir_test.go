package e2e

import (
	"os/exec"
	"strings"
	"testing"
)

// compoundBitwiseIRCases widen the self-host IR subset to the bitwise / shift
// compound-assignment operators: `&= |= ^= <<= >>=` (on bare-ident lvalues —
// fields are immutable and array subscripts read-only, so those lvalue forms
// aren't valid compound targets in either front-end). The native Go parser's
// `compoundOps` map
// (internal/parser/parser.go) has always desugared all ten compound forms to
// `x = x <op> y`, but the self-host parser's `is_compound` recognised only the
// arithmetic five (`+= -= *= /= %=`) — so a program using `x &= y` parsed wrong
// and the module bailed to the AST emitter. parser.fern now mirrors the native
// set exactly, so the bitwise/shift forms desugar to the already-IR-eligible
// binary ops (`& | ^ << >>` all lower through lower_expr) and the whole module
// routes IR.
//
// Each case is oracle-checked against the interpreter and routing-pinned to "ir"
// via asm_pathprobe_run, mirroring self_host_labeled_break_ir_test.go. Results
// stay <= 120 (cf. the wasmtime exit-code gap #2908).
var compoundBitwiseIRCases = []struct {
	name string
	main string
}{
	{"and-eq", `function main(): i32 { var x = 12; x &= 6; return x; }`},
	{"or-eq", `function main(): i32 { var x = 8; x |= 5; return x; }`},
	{"xor-eq", `function main(): i32 { var x = 12; x ^= 10; return x; }`},
	{"shl-eq", `function main(): i32 { var x = 3; x <<= 4; return x; }`},
	{"shr-eq", `function main(): i32 { var x = 100; x >>= 2; return x; }`},
	// the operator should compose with the surrounding control flow.
	{"shl-eq-in-loop", `function main(): i32 { var x = 1; var i = 0; while (i < 5) { x <<= 1; i += 1; } return x; }`},
	{"and-eq-mask-loop", `function main(): i32 { var acc = 0; var i = 0; while (i < 8) { acc |= i; i += 1; } acc &= 7; return acc; }`},
	// regression: the arithmetic compound forms still route IR unchanged.
	{"add-eq-regress", `function main(): i32 { var x = 10; x += 5; return x; }`},
}

// TestSelfHostCompoundBitwiseIRX86_64 routes each case through the self-hosted
// x86-64 IR driver (asm_run), pins the routing to "ir" (asm_pathprobe_run), and
// oracle-checks the exit code against the interpreter.
func TestSelfHostCompoundBitwiseIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range compoundBitwiseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
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

// TestNativeCompoundBitwiseX86_64 is the native-backend half: the same programs
// compiled through the Go compiler's x86-64 emitter must produce the same exit
// codes. The native parser already supported the bitwise/shift compound forms,
// so this pins parity — both front-ends desugar `x <op>= y` identically.
func TestNativeCompoundBitwiseX86_64(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	for _, tc := range compoundBitwiseIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.main+"\n")
			_, code := compileAndRunX86_64(t, tc.main+"\n")
			if code != want {
				t.Errorf("%s native exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
