package e2ecompiler

import (
	"regexp"
	"strings"
	"testing"
)

// The arm64 bounds check must not branch to __fern_oob_abort CONDITIONALLY.
//
// `b.cond` encodes its displacement as R_AARCH64_CONDBR19 — ±1 MiB — and GNU ld
// inserts long-branch veneers for CALL26 (`b` / `bl`, ±128 MiB) but not for
// CONDBR19. __fern_oob_abort lives in the runtime, so once a bounds check sits
// more than 1 MiB away from it the link fails outright:
//
//	relocation truncated to fit: R_AARCH64_CONDBR19 against symbol
//	`__fern_oob_abort' defined in .text section
//
// That is reachable today: the per-module whole-compiler build emits 36 objects,
// and util's are far enough from the runtime's to overflow — the failure names
// __fn_util__base_type_name / __contains / __parse_f64_bits, all array indexing
// (TestSelfHostModloadPerModuleWholeCompilerArm64, #5851). It is a DIFFERENT
// wall from the ±128 MiB CALL26 one closed in June with -ffunction-sections:
// per-function sections do not help, because the limit here is 1 MiB and the
// branch kind is unveneerable.
//
// The shape that works inverts the condition and branches over an unconditional
// `b`, trading one instruction on the non-aborting path for unbounded reach:
//
//	cmp x1, x2
//	b.lo 1f
//	b __fern_oob_abort
//	1:
//
// The whole-compiler link is the end-to-end proof, but it costs ~8 minutes and
// needs arm64 tooling. This is the cheap structural guard: emit the asm and
// check no conditional branch targets the symbol. Structural checks cross-emit
// arm64 text from an x86 host; runtime subtests also assemble and execute it.
//
// SCOPE: array reads and updates are covered. The original constant-index read
// now has a proven bound, so retain it alongside opaque-index controls that must
// still branch to the abort routine. asm_arm64_ir.fern's inline str_slice
// carries the same shape but is NOT covered here: a slice with variable bounds
// lowers to a Fern runtime helper reached by `bl` (CALL26, already
// veneerable), and no probe program emitted the inline form. Rather than ship
// cases that pass vacuously, it is left uncovered and called out.
var condBranchToAbort = regexp.MustCompile(`b\.[a-z]{2}\s+__fern_oob_abort`)

// anyBranchToAbort matches a branch to the abort routine or its leaf adapter.
// Positive controls must contain an actual branch: a bare symbol definition
// would make the assertion pass vacuously.
var anyBranchToAbort = regexp.MustCompile(`\b(b|bl|b\.[a-z]{2})\s+__fern_oob_abort`)

var oobBranchRangeCases = []struct {
	name       string
	src        string
	body       string
	wantBranch bool
	want       int
	runtime    bool
}{
	{"arr-get", "function main(): i32 { let xs: i32[] = [1, 2, 3]; let i: i32 = 2; return xs[i]; }", "main", false, 3, false},
	{"arr-set", "function main(): i32 { let xs: i32[] = [1, 2, 3]; let i: i32 = 1; xs = xs.with(i, 9); return xs[1]; }", "main", true, 9, false},
	{"runtime-arr-get", `@noinline function probe(i: i32): i32 {
    let xs: i32[] = [1, 2, 3];
    return xs[i];
}
function main(): i32 { return probe(args().len() - 2); }`, "probe", true, 0, true},
	{"runtime-arr-set", `@noinline function probe(i: i32): i32 {
    let xs: i32[] = [1, 2, 3];
    xs = xs.with(i, 9);
    return xs[1];
}
function main(): i32 { return probe(args().len() - 2); }`, "probe", true, 0, true},
}

// TestSelfHostOOBBranchRangeArm64 pins that no emitted arm64 bounds check
// branches conditionally to __fern_oob_abort (#5851).
func TestSelfHostOOBBranchRangeArm64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range oobBranchRangeCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "arm64-linux", tc.src)
			body := selfHostFnBody(t, []byte(asm), tc.body)
			if got := anyBranchToAbort.MatchString(body); got != tc.wantBranch {
				t.Errorf("abort branch present=%v, want %v in %s\n%s", got, tc.wantBranch, tc.body, body)
			}
			if !tc.wantBranch && !strings.Contains(body, ".K0") {
				t.Errorf("constant read lost static array control\n%s", body)
			}
			if m := condBranchToAbort.FindAllString(asm, -1); len(m) > 0 {
				t.Errorf("conditional branch to __fern_oob_abort (R_AARCH64_CONDBR19, ±1 MiB — the link overflows once the runtime is far away): %v", m)
			}
			t.Run("run", func(t *testing.T) {
				gcc, qemu := arm64Tooling(t)
				bin := buildBinArm64(t, gcc, t.TempDir(), "prog", asm)
				if !tc.runtime {
					cmd := runArm64Bin(qemu, bin)
					out, _ := cmd.CombinedOutput()
					if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.want {
						t.Fatalf("want exit %d, state=%v\n%s", tc.want, cmd.ProcessState, out)
					}
					return
				}
				// argv includes the program name: zero through four extra arguments
				// supply indices -1, 0, 1, 2 and 3 to the same compiled probe.
				for count, name := range []string{"negative", "first", "middle", "last", "past-end"} {
					t.Run(name, func(t *testing.T) {
						args := make([]string, count)
						for i := range args {
							args[i] = "arg"
						}
						want := count
						if tc.name == "runtime-arr-set" {
							want = 2
							if count == 2 {
								want = 9
							}
						}
						if count == 0 || count == 4 {
							want = 134
						}
						cmd := runArm64Bin(qemu, bin, args...)
						out, _ := cmd.CombinedOutput()
						if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != want {
							t.Errorf("want exit %d, state=%v\n%s", want, cmd.ProcessState, out)
						}
						if want == 134 && !strings.Contains(string(out), "fern: array index out of range") {
							t.Errorf("missing array bounds diagnostic\n%s", out)
						}
					})
				}
			})
		})
	}
}
