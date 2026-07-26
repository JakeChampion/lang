package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostLargeTierAllocIRX86_64 exercises the #3425 large-tier allocator
// (asm_ir.fern's __fern_alloc large pop + __fern_large_class / __fern_large_push,
// backed by __fern_large_heads). Blocks the 65536-word small tier can't class
// (>= 512 KiB) previously LEAKED into the bump arena on every free; now they are
// recycled via power-of-two classes. The program:
//   - builds a 70000-element array (a > 512 KiB block → large tier),
//   - reads an element (proves the block is valid after the large-bump path),
//   - drops it and builds another same-size array (arr_dec's large PUSH then
//     __fern_alloc's large POP-hit reuse the freed block),
//   - reads from the SECOND array — a wrong value here would mean the reused
//     block was mis-classed / too small (heap corruption).
// Returns 42 iff both reads are correct. Hardcoded oracle (no interp — 140k
// appends are slow to tree-walk); run via the X86_64Tooling runner since it
// heap-allocates.
func TestSelfHostLargeTierAllocIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const src = `function build(n: i32): i32[] {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < n) { a = a.append(i * 2); i = i + 1; }
    return a;
}
function main(): i32 {
    var a: i32[] = build(70000);
    var x: i32 = a[69999];
    a = build(70000);
    var y: i32 = a[35000];
    if (x == 139998 && y == 70000) { return 42; }
    return 0;
}`
	asm := runCapture(t, gcc, runner, driverBin, []byte(src), "-ir")
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "large_tier", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("large-tier alloc exited %d, want 42 (large-block reuse mis-classed / corrupted?)", code)
	}
}
