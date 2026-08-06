package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A comparison picks the signed or unsigned form from whether its operands are
// u64. `expr_is_u64` recognised a u64 local, array element, struct field, cast
// and generic result — but not a u64 LITERAL, so `9223372036854776423u64 <= 17u64`
// compiled to a SIGNED compare. Above 2^63 bit 63 is set, the value reads
// negative, and the answer comes out backwards (#6178, fernsmith seed 127).
//
// Invisible below 2^63, where signed and unsigned agree, which is why it lasted:
// only a literal past the signed ceiling can show it, and only in the operand
// position that has no name to hang a type on.
//
// Mutation-measured by removing the ExprNumber arm and rebuilding: the four
// past-2^63 cases answer backwards, the two below-the-ceiling / signed pins
// pass either way. `u64_local_same_answer` routes the same magnitude through a
// u64-RETURNING function instead of a literal — the call arm already resolved
// that — so it pins the literal and the call to the same answer rather than
// testing the fix. Oracle: the interpreter.
var u64LiteralCompareCases = []struct {
	name string
	expr string
}{
	// Seed 127's shape: a u64 literal past 2^63 against a small one.
	{"gt_2_63_le_small", "(if ((9223372036854776423u64 <= 17u64)) { 1 } else { 2 })"},
	{"small_lt_gt_2_63", "(if ((191u64 < 9223372036854776520u64)) { 1 } else { 2 })"},
	{"gt_2_63_gt_small", "(if ((9223372036854776423u64 > 17u64)) { 1 } else { 2 })"},
	{"gt_2_63_ge_small", "(if ((17u64 >= 9223372036854776423u64)) { 1 } else { 2 })"},

	// Below 2^63 the two compares agree — this was never wrong and must stay right.
	{"below_2_63_unaffected", "(if ((17u64 <= 4611686018427387904u64)) { 1 } else { 2 })"},
	// An i64 literal is SIGNED; the fix must not make every wide literal unsigned.
	{"i64_literal_stays_signed", "(if (((0 - 5i64) < 3i64)) { 1 } else { 2 })"},
	// The same magnitude through a u64-returning CALL, which resolved correctly
	// before this fix — pins the literal and the call to the same answer.
	{"u64_local_same_answer", "(if ((__u() <= 17u64)) { 1 } else { 2 })"},
}

// TestSelfHostU64LiteralCompareIR_X86_64 pins signed-vs-unsigned selection for
// comparisons whose u64-ness comes from a literal (#6178), on the self-host IR
// path.
func TestSelfHostU64LiteralCompareIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, interpBin := annotateF64ProjDir(t)
	_, runner := x86_64Tooling(t)

	for _, tc := range u64LiteralCompareCases {
		t.Run(tc.name, func(t *testing.T) {
			src := "function __u(): u64 { return 9223372036854776423u64; }\n" +
				"function main(): i32 { return " + tc.expr + "; }\n"
			want := interpExit(t, interpBin, src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			route, derr := exec.Command(mmc, mainPath, stdlibRoot, "-decide").Output()
			if derr != nil {
				t.Fatalf("route decide: %v", derr)
			}
			if got := strings.TrimSpace(string(route)); got != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" (case no longer exercises the IR path)", tc.name, got)
			}
			asm, cerr := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if cerr != nil {
				t.Fatalf("loader compile: %v", cerr)
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "u64lit_"+tc.name, string(asm))
			argv := append(append([]string{}, runner...), progBin)
			cmd := exec.Command(argv[0], argv[1:]...)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle) — `%s` compared signed",
					tc.name, code, want, tc.expr)
			}
		})
	}
}
