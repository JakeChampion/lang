package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostArrPushOwnedReclaimArm64 covers the arm64 port of the sole-owner
// self-append reclaim-on-grow (`__fern_arr_push_owned`): the unaliased
// `a = a.append(v)` form now frees the dead old buffer when the push reallocates,
// instead of leaking it (arm64 previously aliased arr_push_owned to plain
// __fern_arr_push). Verified by CORRECTNESS under qemu — a wrong free of the live
// growing buffer is a use-after-free that corrupts the sum — plus an asm-content
// check that the owned helper is dispatched (`bl __fern_arr_push_owned`) and its
// real reclaim body (the freelist push `str x2, [x3, x1, lsl #3]`) is emitted.
func TestSelfHostArrPushOwnedReclaimArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm_arm64_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_run.fern", "driver")

	// 100 sole-owner self-appends grow the buffer several times; each grow's old
	// buffer is reclaimed. A UAF of the live buffer would corrupt the read-back.
	// sum(0..99) = 4950, so the program returns 7.
	prog := `function build(): i32 {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < 100) { a = a.append(i); i = i + 1; }
    var sum: i32 = 0;
    var j: i32 = 0;
    while (j < a.len()) { sum = sum + a[j]; j = j + 1; }
    return sum;
}
function main(): i32 { return build() - 4943; }`

	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog))
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes")
	}
	if !strings.Contains(string(asm), "bl __fern_arr_push_owned") {
		t.Error("arr_push_owned not dispatched on arm64 (a = a.append(v) did not lower to the owned self-append)")
	}
	if !strings.Contains(string(asm), "str x2, [x3, x1, lsl #3]") {
		t.Error("real __fern_arr_push_owned reclaim body (freelist push) not found in arm64 asm")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "arr_push_owned_arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("self-append reclaim exited %d, want 7 (sum 0..99 = 4950) — reclaim corrupted the live buffer?", code)
	}
}
