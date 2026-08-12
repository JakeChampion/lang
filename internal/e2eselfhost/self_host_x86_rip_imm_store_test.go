package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostX86RipImmediateStoreLinks pins `mov $imm, sym(%rip)` through the
// self-host's IN-PROCESS x86 assembler (#6764).
//
// The distinction is the whole test. `x86_native.fern` encoded every other
// rip-relative shape and refused the immediate store, so the strbuf runtime —
// which opens `__fern_strbuf_reset` with `movq $0, __fern_strbuf_len(%rip)` —
// emitted correct assembly that the assembler then could not consume. Every
// existing x86 self-host program test hands the emitted `.s` to gcc, so the
// in-process path this one drives was the only place the bug lived, and the
// `fern` driver itself (which needs strbuf) could not be linked by the
// self-host at all.
//
// The program builds TWO strings, because the failure mode is an off-by-four
// displacement rather than a missing store: a rip displacement is measured
// from the end of the instruction, and this is the one shape whose instruction
// continues past its disp32. A store four bytes off leaves the builder's
// length un-reset, which the first `strbuf_take` cannot see and the second
// reports as leftover bytes.
func TestSelfHostX86RipImmediateStoreLinks(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("in-process link + run needs a native host (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	const src = `function main(): i32 {
    strbuf_reset();
    strbuf_append("ab");
    strbuf_append("cd");
    var first: string = strbuf_take();
    strbuf_append("xy");
    var second: string = strbuf_take();
    print(first);
    print(second);
    if (first.len() != 4) { return 1; }
    if (second.len() != 2) { return 2; }
    return 0;
}`

	srcPath := filepath.Join(dir, "ripimm.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	progBin := filepath.Join(dir, "ripimm")
	// `-o` links in process: no `.s`, no gcc, no ld — the path the refusal
	// aborted with `rip-relative operand not encoded here`.
	if out, err := exec.Command(fernBin, "-target", "x86-64-linux", "-o", progBin, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("self-host in-process link failed: %v\n%s", err, out)
	}

	run := exec.Command(progBin)
	out, err := run.Output()
	if err != nil {
		t.Fatalf("linked program failed: %v\n%s", err, out)
	}
	if want := "abcd\nxy\n"; string(out) != want {
		t.Errorf("program stdout = %q, want %q — a stale builder length means the store landed at the wrong displacement", out, want)
	}
}
