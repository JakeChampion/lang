package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin #4554: the legacy AST x86-64 and arm64 backends stored
// struct-literal field values POSITIONALLY (literal index → slot), while
// ExprFieldAccess reads at DECLARATION-order offsets — so a literal written
// out of decl order silently scrambled every field from the first mismatch
// on. This is exactly how #4552's LowerState literal (clo_cap_kinds listed
// at a different position than its declaration) corrupted the stage-2
// bootstrap: `aliased_names` read back `clo_cap_kinds`' value, flipping
// arr_push/arr_push_owned decisions, and the scalar-field variant segfaulted.
// The wasm legacy backend and the IR path always mapped by name.
//
// The programs append 520 trivial pad functions to blow the IR path's
// 512-function module budget, forcing the WHOLE module through the legacy
// AST emitter (the same route the bootstrap self-compile takes) — a small
// program would otherwise ride the IR path and never reach the buggy code.
const padFuncs = 520

func padProgram(core string) string {
	var b strings.Builder
	b.WriteString(core)
	b.WriteString("\n")
	for i := 0; i < padFuncs; i++ {
		fmt.Fprintf(&b, "function pad%d(x: i32): i32 { return x + %d; }\n", i, i)
	}
	return b.String()
}

var structLitOrderCases = []struct {
	name string
	core string
	want int
}{
	// The minimal discriminator: literal lists b before a. Positional
	// stores put 7 in a's slot → 71; decl-order mapping → 17.
	{"two-field-swapped",
		`struct P { a: i32, b: i32 }
function main(): i32 { var p: P = P { b: 7, a: 1 }; return p.a * 10 + p.b; }`, 17},
	// Partial reorder around an in-place middle field.
	{"three-field-rotated",
		`struct T { a: i32, b: i32, c: i32 }
function main(): i32 { var t: T = T { c: 3, a: 1, b: 2 }; return t.a * 100 + t.b * 10 + t.c; }`, 123},
	// Pointer-shaped fields out of order: the retain (rc-inc) path must
	// use the mapped slot too, and reads of string/array fields must land
	// on the right boxes (a scramble here reads a string as an array —
	// the #4554 pointer-field silent-corruption shape).
	{"mixed-pointer-fields-swapped",
		`struct Q { s: string, n: i32, xs: i32[] }
function main(): i32 {
    var base: i32[] = [1, 2, 3];
    var q: Q = Q { xs: base, n: 5, s: "ab" };
    return q.s.len() * 100 + q.n * 10 + q.xs.len();
}`, 253},
	// Struct-update spread: the has_base path always mapped override names
	// to decl slots — pinned here so the two literal paths stay consistent.
	// (Values kept under 256 — the contract is the process exit code.)
	{"struct-update-out-of-order-overrides",
		`struct R { a: i32, b: i32, c: i32 }
function main(): i32 {
    var r0: R = R { a: 1, b: 2, c: 3 };
    var r1: R = R { ...r0, c: 9, a: 2 };
    return r1.a * 100 + r1.b * 10 + r1.c;
}`, 229},
}

// TestSelfHostStructLitOrderLegacyX86_64 cross-checks each case against the
// native backend, then runs it through the self-host driver with the module
// forced onto the legacy AST x86-64 emitter.
func TestSelfHostStructLitOrderLegacyX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "checker.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range structLitOrderCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := padProgram(tc.core)
			if _, code := compileAndRunX86_64(t, prog); code != tc.want {
				t.Fatalf("%s native exited %d, want %d", tc.name, code, tc.want)
			}
			asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			// The padding must actually force the legacy route — if the IR
			// budget ever rises past padFuncs the test would silently stop
			// covering the legacy emitter.
			if strings.Contains(string(asm), ".Lir_main") {
				t.Fatalf("%s routed main through the IR path — raise padFuncs above the IR module budget", tc.name)
			}
			bin := buildBin(t, gcc, dir, "slo-"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s legacy-AST x86-64 exited %d, want %d (a scramble means positional field stores — #4554)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostStructLitOrderLegacyArm64 runs the same cases through the
// legacy arm64 emitter under qemu (asm_arm64.fern mirrored the positional
// store).
func TestSelfHostStructLitOrderLegacyArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "checker.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range structLitOrderCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := padProgram(tc.core)
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "slo-"+tc.name+"-arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s legacy-AST arm64 exited %d, want %d (a scramble means positional field stores — #4554)", tc.name, code, tc.want)
			}
		})
	}
}
