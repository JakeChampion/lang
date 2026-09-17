package e2eselfhost

import (
	"os/exec"
	"testing"
)

// `xs.join(sep)` has to be LINEAR in the joined length, and nothing asserted
// that for the self-host. `__fern_arr_str_join` built its answer with
// `r = r + xs[i]`, which recopies the whole prefix per element — O(total²),
// the hazard native's own `__method_Array_join` comment names and the reason
// that one sizes an exact buffer first.
//
// It hid because every existing gate was blind to it. The answer is CORRECT
// either way, so the corpus could not see it. The allocation-differential
// gate could not either: the intermediates are all freed, so the freelist
// hands them back and `__heap_bump_bytes` reads 0 KB on both compilers at
// every size — verified at n=400 and n=20000, before and after the fix. What
// separates the two is time and peak memory, so this case asserts the answer
// at a SIZE THE QUADRATIC CANNOT REACH: 100k parts is 8 ms once join is
// linear, and was OOM-killed before (n=50000 took 4.2 s, n=100000 died).
//
// `std/io.read_all_stdin` is why the size matters rather than the shape: it
// collects chunks and joins them once, which is the idiom the stdlib
// documents, so every utility that reads standard input goes through this
// helper.
//
// The length is checked rather than the bytes: 100000 * len("item") + 99999
// separators. The first byte is checked too, so a join that returned an empty
// or truncated buffer of the right length would still fail.
const arrStrJoinScaleSrc = `function main(): i32 {
    var parts: string[] = [];
    var i: i32 = 0;
    while (i < 100000) { parts = parts.append("item"); i = i + 1; }
    var j: string = parts.join(",");
    if (j.len() != 499999) { return 1; }
    if (j[0] as i32 != 105) { return 2; }
    if (j[4] as i32 != 44) { return 3; }
    return 42;
}`

// TestSelfHostArrStrJoinScaleX86_64 runs it through the production x86-64 IR
// path, the same driver the append-borrowed cases use.
func TestSelfHostArrStrJoinScaleX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := runCapture(t, gcc, runner, driverBin, []byte(arrStrJoinScaleSrc), "-ir")
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "arr-str-join-scale", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("joined 100k parts exited %d, want 42 (137 is the OOM the quadratic form died of)", code)
	}
}
