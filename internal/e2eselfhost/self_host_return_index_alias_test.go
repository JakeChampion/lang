package e2eselfhost

import (
	"os/exec"
	"testing"
)

// returnIndexAliasCases pin the return-transfer retain for `return xs[i]`
// where the element is itself an rc-counted array (native: needsRcIncOnAlias's
// Index arm; self-host: irlower's index_read_is_arr return branch). The callee
// hands the caller an ALIAS of an element the container still owns; without
// the retain the caller's exit sweep decs a count the container holds, so the
// element's box is freed under the live container and reused by the next
// allocation. That is exactly how std/fuzz's __fuzz_pick_seed corrupted its
// seed corpus (#7474: `input.reverse().reverse()` diverging on the stdtest
// differential): each loop iteration over-released one seeds[i], and mutation
// churn then rewrote the freed cells. The junk allocations after the loop
// reproduce that churn, so a regression reads reused garbage (exit 90-93)
// or trips the underflow detector (exit 99) instead of exiting 0.
var returnIndexAliasCases = []struct {
	name string
	src  string
	exit int
}{
	// The fuzz shape: borrowed T[][] param, element returned by index.
	{"param-container", `function pick(seeds: i32[][], i: i32): i32[] {
    return seeds[i % seeds.len()];
}
function main(): i32 {
    var seeds: i32[][] = [[7], [1, 2, 3, 4, 5]];
    var k: i32 = 0;
    while (k < 4) {
        var seed: i32[] = pick(seeds, k);
        if (seed.len() == 0) { return 80; }
        k = k + 1;
    }
    var junk: i32[] = [9, 9, 9, 9, 9, 9, 9, 9];
    var junk2: i32[] = [8, 8, 8, 8, 8, 8, 8, 8];
    if (seeds[0].len() != 1) { return 90; }
    if (seeds[0][0] != 7) { return 91; }
    if (seeds[1].len() != 5) { return 92; }
    if (seeds[1][4] != 5) { return 93; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// string[][] element: the retain is on the element's own array box,
	// element kind irrelevant.
	{"strarr-element", `function pickrow(m: string[][], i: i32): string[] {
    return m[i];
}
function main(): i32 {
    var m: string[][] = [["a", "b"], ["c", "d", "e"]];
    var k: i32 = 0;
    while (k < 4) {
        var row: string[] = pickrow(m, k % 2);
        if (row.len() == 0) { return 80; }
        k = k + 1;
    }
    var junk: i32[] = [9, 9, 9, 9, 9, 9, 9, 9];
    if (m[0].len() != 2) { return 90; }
    if (m[1].len() != 3) { return 91; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
	// Negative balance check: a LOCAL container swept at exit must not
	// over-release the escaping element the retain now covers.
	{"local-container", `function first_row(): i32[] {
    var m: i32[][] = [[1, 2], [3, 4, 5]];
    return m[0];
}
function main(): i32 {
    var k: i32 = 0;
    while (k < 4) {
        var r: i32[] = first_row();
        var junk: i32[] = [9, 9, 9, 9];
        if (r.len() != 2) { return 90; }
        if (r[1] != 2) { return 91; }
        k = k + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, 0},
}

// TestSelfHostReturnIndexAliasX86_64 drives the cases through the production
// x86-64 IR path (asm_ir_run `-ir`).
func TestSelfHostReturnIndexAliasX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range returnIndexAliasCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d (90-93 = corrupt read via freed-then-reused element; 99 = underflow)", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostReturnIndexAliasArm64 — arm64 counterpart; the retain is shared
// irlower analysis, so both register backends inherit it.
func TestSelfHostReturnIndexAliasArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range returnIndexAliasCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d (90-93 = corrupt read via freed-then-reused element; 99 = underflow)", tc.name, code, tc.exit)
			}
		})
	}
}
