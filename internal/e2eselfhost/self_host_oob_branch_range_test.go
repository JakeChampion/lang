package e2eselfhost

import (
	"os"
	"path/filepath"
	"regexp"
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
// check no conditional branch targets the symbol. It needs no arm64 toolchain at
// all — the self-host compiler cross-emits arm64 text from an x86 host — so it
// fails in seconds rather than at link time in a slow lane.
//
// SCOPE: these two cases are the sites reachable on the IR path, and both were
// verified to fail without the fix. asm_arm64_ir.fern's inline str_slice and
// asm_arm64.fern's __fern_arr_slice helper carry the same `b.hi` shape and were
// fixed alongside, but are NOT covered here: a slice with variable bounds lowers
// to a Fern runtime helper reached by `bl` (CALL26, already veneerable), and no
// probe program emitted the inline form. Rather than ship cases that pass
// vacuously, they are left uncovered and called out.
var condBranchToAbort = regexp.MustCompile(`b\.[a-z]{2}\s+__fern_oob_abort`)

// anyBranchToAbort matches a branch of any kind to the symbol. A case that emits
// none guards nothing — the `.globl __fern_oob_abort` DEFINITION is emitted
// unconditionally, so testing for the bare symbol name would pass vacuously.
var anyBranchToAbort = regexp.MustCompile(`\b(b|bl|b\.[a-z]{2})\s+__fern_oob_abort`)

var oobBranchRangeCases = []struct {
	name string
	src  string
}{
	{"arr-get", "function main(): i32 { var xs: i32[] = [1, 2, 3]; var i: i32 = 2; return xs[i]; }"},
	{"arr-set", "function main(): i32 { var xs: i32[] = [1, 2, 3]; var i: i32 = 1; xs[i] = 9; return xs[1]; }"},
}

// TestSelfHostOOBBranchRangeArm64 pins that no emitted arm64 bounds check
// branches conditionally to __fern_oob_abort (#5851).
func TestSelfHostOOBBranchRangeArm64(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range oobBranchRangeCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64"))
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			if !anyBranchToAbort.MatchString(asm) {
				t.Fatal("no branch to __fern_oob_abort — the bounds check is gone, so this case guards nothing")
			}
			if m := condBranchToAbort.FindAllString(asm, -1); len(m) > 0 {
				t.Errorf("conditional branch to __fern_oob_abort (R_AARCH64_CONDBR19, ±1 MiB — the link overflows once the runtime is far away): %v", m)
			}
		})
	}
}
