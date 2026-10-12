package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSelfHostX86RipImmediateStoreLinks pins `mov $imm, sym(%rip)` through the
// self-host's IN-PROCESS x86 assembler (#6764).
//
// `x86_native.fern` encoded every other rip-relative shape and refused the
// immediate store, so a runtime helper opening with `movq $0, sym(%rip)`
// emitted correct assembly that the assembler then could not consume. Every
// x86 self-host program test that hands the emitted `.s` to gcc misses that,
// so this one links in process.
//
// The failure mode worth pinning is an off-by-four displacement rather than a
// refusal: a rip displacement is measured from the end of the instruction,
// and this is the one shape whose instruction continues past its disp32. The
// leak census's report clears `__fern_args_set` with exactly this store once
// a program has called set_args, and a store four bytes off changes nothing
// the program prints, so the test reads the linked binary back with objdump
// and requires the store's resolved target to be the symbol itself.
func TestSelfHostX86RipImmediateStoreLinks(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("in-process link + run needs a native host (argv paths)")
	}
	objdump, err := exec.LookPath("objdump")
	if err != nil {
		t.Skip("objdump not on PATH")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	const src = `function main(): i32 {
    set_args(["ripimm", "x"]);
    if (args().len() != 2) { return 1; }
    return 0;
}`

	srcPath := filepath.Join(dir, "ripimm.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	progBin := filepath.Join(dir, "ripimm")
	// `-o` links in process: no `.s`, no gcc, no ld — the path the refusal
	// aborted with `rip-relative operand not encoded here`.
	build := exec.Command(fernBin, "-g", "-target", "x86-64-linux", "-o", progBin, srcPath)
	build.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("self-host in-process link failed: %v\n%s", err, out)
	}

	if out, err := exec.Command(progBin).CombinedOutput(); err != nil {
		t.Fatalf("linked program failed: %v\n%s", err, out)
	}

	dis, err := exec.Command(objdump, "-d", progBin).Output()
	if err != nil {
		t.Fatalf("objdump: %v", err)
	}
	// The report reads the word with an ordinary rip-relative load a few
	// instructions before it clears it, and a load's displacement ends the
	// instruction, so its target is the symbol's address: the store must
	// resolve to the same one. objdump prints each resolved target after '#'.
	load := regexp.MustCompile(`\smov\s+-?0x[0-9a-f]+\(%rip\),%rax\s+# ([0-9a-f]+)`)
	store := regexp.MustCompile(`\smovq\s+\$0x0,-?0x[0-9a-f]+\(%rip\)\s+# ([0-9a-f]+)`)
	lines := strings.Split(string(dis), "\n")
	checked := 0
	for i, line := range lines {
		m := store.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for j := i - 1; j >= 0 && j >= i-4; j-- {
			if l := load.FindStringSubmatch(lines[j]); l != nil {
				checked++
				if l[1] != m[1] {
					t.Errorf("the immediate store resolves to %s, the load of the same word to %s: the displacement ignores the trailing immediate\n%s\n%s", m[1], l[1], lines[j], line)
				}
				break
			}
		}
	}
	if checked == 0 {
		t.Fatal("no `movq $0x0, …(%rip)` store preceded by a load of the same word in the linked binary; the leak census's report no longer has the shape this test needs")
	}
}
