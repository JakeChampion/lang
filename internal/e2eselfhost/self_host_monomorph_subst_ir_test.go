package e2eselfhost

import (
	"bytes"
	"os/exec"
	"testing"
)

// monomorphSubstCases pin parser.subst_expr's coverage: when a bounded generic
// is monomorphised, every reference to the type PARAMETER in the cloned body
// has to become the concrete type.
//
// The only way a bare type-param name appears as an expression is as the object
// of an associated call — `T.zero()` — and the substitution used to recurse
// through eight expression shapes and stop. A `T.zero()` nested inside an ARRAY
// LITERAL or a SLICE kept the name `T` into the clone, and lowering then could
// not resolve it:
//
//	FERN_STRICT_IR: __arrm_total__i32 (function value T not defined)
//
// The module fell off the IR path entirely, so the failure is a refused
// compile, not a wrong answer. Native compiles and runs all three.
//
// `plain` is the control — the same call in a `var` initialiser, an arm the
// substitution always had — so a regression that broke substitution outright
// fails all three rows rather than looking like this bug.
//
// std/num rather than a locally-declared trait: a user-declared trait's
// associated function reached through a bound type parameter does not lower on
// the self-host at all yet (`function value i32 not defined`), so a local trait
// cannot isolate this.
var monomorphSubstCases = []struct {
	name string
	src  string
}{
	{"plain", "import \"std/num\" as num;\n" +
		"function (a: T[]) total[T: num.Num + num.Zero](): T { var s: T = T.zero(); var i: i32 = 0; while (i < a.len()) { s = s.add(a[i]); i = i + 1; } return s; }\n" +
		"function main(): i32 { var xs: i32[] = [40, 2]; return xs.total(); }\n"},
	{"in-array-literal", "import \"std/num\" as num;\n" +
		"function (a: T[]) total[T: num.Num + num.Zero](): T { var seed: T[] = [T.zero()]; var s: T = seed[0]; var i: i32 = 0; while (i < a.len()) { s = s.add(a[i]); i = i + 1; } return s; }\n" +
		"function main(): i32 { var xs: i32[] = [40, 2]; return xs.total(); }\n"},
	{"in-slice", "import \"std/num\" as num;\n" +
		"function (a: T[]) total[T: num.Num + num.Zero](): T { var seed: T[] = [T.zero(), T.zero()]; var s: T = seed[0:1][0]; var i: i32 = 0; while (i < a.len()) { s = s.add(a[i]); i = i + 1; } return s; }\n" +
		"function main(): i32 { var xs: i32[] = [40, 2]; return xs.total(); }\n"},
}

func TestSelfHostMonomorphSubstIRX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	interpBin := buildLangBinForInterp(t)

	for _, tc := range monomorphSubstCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm, progDir := compileSourceModload(t, runner, driverBin, tc.src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, progDir, "msub_"+tc.name, asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			cmd.Stdin = bytes.NewReader(nil)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (native interp oracle)", tc.name, code, want)
			}
		})
	}
}
