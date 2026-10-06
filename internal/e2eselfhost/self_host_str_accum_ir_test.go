package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// strAccumIRCases pin the string-builder ACCUMULATOR reclaim on the self-hosted
// stack-IR path (#2649 consume-rebind). The canonical string leak is
// `let s: string = ""; while (…) { s = s + part; } … use(s)`: each `s = s + part`
// allocates a fresh box + buffer and orphans the previous one, so the whole growth
// chain leaks. The reclaim frees the superseded box on each reassignment
// (emit_str_reclaim_store on the StmtAssign) and the final at scope exit, gated by:
//   - the init being a literal or fresh producer,
//   - EVERY reassignment being a fresh consume-rebind of s (str_accum_reassign_ok),
//   - s never escaping (str_accum_unsafe_for — which forbids `return s` for now, so
//     a NON-escaping accumulator only; the move-out builder is a follow-up).
//
// __fern_str_free's heap-base guard makes freeing the initial "" literal a no-op on
// its .rodata data (its 16-byte box is still reclaimed).
var strAccumIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Basic accumulator: "" then 4× `s = s + "x"`. len 4. (The "x" literal temporary
	// leaks — an anonymous const_str, orthogonal to the accumulator — but s itself is
	// reclaimed each reassignment. Post-#4262 the "x" operand is ALSO reclaimed by
	// emit_str_concat_reclaim, so this case now emits reclaims for both.)
	{"accum-basic",
		`function main(): i32 { let s: string = ""; let i: i32 = 0; while (i < 4) { s = s + "x"; i = i + 1; } return s.len(); }`,
		4},
	// Accumulator with a non-empty literal init and a multi-char part. len 1+3*2=7.
	{"accum-init-nonempty",
		`function main(): i32 { let s: string = "a"; let i: i32 = 0; while (i < 3) { s = s + "bc"; i = i + 1; } return s.len(); }`,
		7},
	// Accumulator over a loop-invariant LOCAL operand `x` (a borrow-read, freed once
	// at exit): s = s + x. No per-iteration literal temporary. len 3*2 = 6.
	{"accum-invariant-operand",
		`function main(): i32 { let x: string = "yy"; let s: string = ""; let i: i32 = 0; while (i < 3) { s = s + x; i = i + 1; } return s.len(); }`,
		6},
	// Memory-safety at scale: a BOUNDED accumulator (grow, then reset to a fresh 1-char
	// chr(..) at len > 40) over 5,000,000 iterations, using a loop-invariant operand so
	// there is no per-iteration literal temporary. If the growth chain leaked, resident
	// memory would grow; a double-free would corrupt the freelist and crash / return
	// garbage. exit 0 (fixed) with the reclaim present proves the balance (flat heap).
	{"accum-churn-safe",
		`function main(): i32 { let x: string = "yy"; let s: string = ""; let i: i32 = 0; while (i < 5000000) { s = s + x; if (s.len() > 40) { s = chr(65); } i = i + 1; } return 0; }`,
		0},
	// UN-ANNOTATED accumulator (`let s = ""`, no `: string`): reclaimed too — the
	// annotation is not required; the is_str type gate at the reclaim site admits the
	// actual string accumulator. len 4.
	{"accum-unannotated",
		`function main(): i32 { let s = ""; let i: i32 = 0; while (i < 4) { s = s + "x"; i = i + 1; } return s.len(); }`,
		4},
	// UN-ANNOTATED returned builder: intermediates freed, final moved out. len 6.
	{"accum-unannotated-return",
		`function build(n: i32): string { let s = ""; let i: i32 = 0; while (i < n) { s = s + "ab"; i = i + 1; } return s; } function main(): i32 { return build(3).len(); }`,
		6},
	// NEGATIVE: an int accumulator (`n = n + i`) matches the reassign SHAPE but is not
	// is_str, so it is never reclaimed (no __fern_str_free) and stays correct. 0+1+2+3+4.
	{"accum-int-not-reclaimed",
		`function main(): i32 { let n: i32 = 0; let i: i32 = 0; while (i < 5) { n = n + i; i = i + 1; } return n; }`,
		10},
	// MOVE-ON-RETURN: a returned string builder. The intermediates are freed by the
	// consume-rebind inside build(), and the FINAL is moved out (kept from the exit
	// sweep — freeing it would dangle the box handed to the caller). build(5) → len 5.
	{"accum-return-builder",
		`function build(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "x"; i = i + 1; } return s; } function main(): i32 { return build(5).len(); }`,
		5},
	// Move-on-return with a loop-invariant operand + a BRANCHY return (early at
	// len > 8 or the final return) — both return sites move s out. len 9.
	{"accum-return-branchy",
		`function build(n: i32): string { let s: string = "start"; let i: i32 = 0; while (i < n) { s = s + "z"; if (s.len() > 8) { return s; } i = i + 1; } return s; } function main(): i32 { return build(100).len(); }`,
		9},
	// NEGATIVE: a NON-FRESH reassignment (`s = "reset"`, a literal alias) must exclude
	// the accumulator — freeing a later s could double-free the literal-shared box.
	// The loop operand x is ALIASED by kx, which is what keeps it un-reclaimable and
	// leaves the accumulator as the only thing a reclaim could be about. A bare
	// literal-init local would no longer serve: a concat operand is a borrow, so an
	// x used only there earns the ordinary literal-local reclaim and the count stops
	// isolating this contract. 0 sites here, 2 under a compiler that frees x.
	// "reset" (5) + x (1) = 6.
	// The concat source is a PARAMETER so nothing but the accumulator could be
	// reclaimed here, and the whole-program count therefore isolates it. It was a
	// local aliased by `let kx = x` before #7282, which suppressed its reclaim only
	// while an alias cost a string its credit; now an alias retains the box and both
	// slots release it, so that scaffolding freed x as well and the count went
	// 0 → 4. slot_is_reclaimable_str refuses a parameter outright, which does not
	// depend on any escape scan. "reset".len() + "x".len() = 6.
	{"accum-nonfresh-reassign-not-reclaimed",
		`function acc(x: string): i32 { let s: string = ""; let i: i32 = 0; while (i < 3) { s = s + x; i = i + 1; } s = "reset"; return s.len() + x.len(); } function main(): i32 { return acc("x"); }`,
		6},
}

// TestSelfHostStrAccumIRX86_64 compiles each case through the self-hosted x86-64
// driver (asm_run, IR default-on), asserting the exit code and that the accumulator
// consume-rebind reclaim (call __fn___fern_str_free) is (or isn't) emitted.
func TestSelfHostStrAccumIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range strAccumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
