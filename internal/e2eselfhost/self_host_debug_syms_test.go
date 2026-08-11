package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestSelfHostDebugSymsFlag covers `fern-selfhost -target x86-64 -g` end to
// end (#6637): a binary the self-host built, whose function names `nm` can
// resolve. Before this, every self-host-built binary was anonymous — which
// bit the project itself hardest, since the self-hosted compiler is the
// largest program it builds with itself and the one whose segfaults most
// need a readable backtrace.
//
// Three things are checked, and the second and third matter as much as the
// first:
//
//   - `nm` resolves the emitted function names, with non-zero sizes. A size
//     of 0 resolves an exact entry point and nothing else, so a backtrace
//     could not attribute an address that fell inside a function.
//   - the -g binary still RUNS. Appending a section-header table must not
//     change what the loader maps.
//   - WITHOUT -g the image is unchanged: no section headers at all. That is
//     what keeps the flag from being a tax on every ordinary build.
func TestSelfHostDebugSymsFlag(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("emitted binaries are executed directly; skipping under an exec runner")
	}
	if _, err := exec.LookPath("nm"); err != nil {
		t.Skip("nm not on PATH; skipping symbol read-back")
	}

	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	src := filepath.Join(dir, "p.fern")
	if err := os.WriteFile(src, []byte(
		"function helper(n: i32): i32 { return n + 3; }\n"+
			"function main(): i32 { return helper(4); }\n"), 0o644); err != nil {
		t.Fatalf("writing source: %v", err)
	}
	stdlib := langSrcAbs(t, "internal/stdlib")

	build := func(out string, extra ...string) string {
		args := extra
		if !slices.Contains(args, "-target") {
			args = append([]string{"-target", "x86-64"}, args...)
		}
		args = append(args, "-o", out, src, stdlib)
		cmd := exec.Command(cli, args...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fern-selfhost %v: %v\n%s", args, err, b)
		}
		return out
	}

	plain := build(filepath.Join(dir, "plain.bin"))
	withG := build(filepath.Join(dir, "withg.bin"), "-g")

	// Both run, and to the same answer: -g changes the file, not the program.
	for _, bin := range []string{plain, withG} {
		if err := os.Chmod(bin, 0o755); err != nil {
			t.Fatalf("chmod %s: %v", bin, err)
		}
		run := exec.Command(bin)
		_ = run.Run()
		if run.ProcessState == nil || !run.ProcessState.Exited() {
			t.Fatalf("%s did not exit normally", filepath.Base(bin))
		}
		if code := run.ProcessState.ExitCode(); code != 7 {
			t.Errorf("%s exit = %d, want 7", filepath.Base(bin), code)
		}
	}

	// nm resolves the user's functions in the -g build.
	out, err := exec.Command("nm", withG).Output()
	if err != nil {
		t.Fatalf("nm: %v", err)
	}
	for _, want := range []string{"__fn_helper", "__fn_main", "_start"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("nm output missing %q\n--- got ---\n%s", want, out)
		}
	}
	// Every symbol is typed as a function ("T" is .text/global in nm's
	// output); a table of untyped symbols would still contain the names.
	if n := strings.Count(string(out), " T "); n < 3 {
		t.Errorf("expected at least 3 text symbols, got %d\n%s", n, out)
	}

	// And nm on the plain build resolves nothing, because there is no
	// symbol table to read.
	plainOut, _ := exec.Command("nm", plain).CombinedOutput()
	if strings.Contains(string(plainOut), "__fn_main") {
		t.Errorf("the non-g build carries symbols; -g should be opt-in\n%s", plainOut)
	}

	// arm64 reaches the ELF layer through a different writer — the W^X
	// two-segment image, where .text sits past TWO program headers rather
	// than one. That is the case an x86-only test cannot cover, and the
	// reason the symtab layer takes the offset and vaddr from its caller.
	//
	// nm reads a foreign architecture happily, so the symbols are checked
	// without needing qemu; execution stays x86-only above.
	a64 := build(filepath.Join(dir, "a64.bin"), "-target", "arm64", "-g")
	a64Out, err := exec.Command("nm", a64).Output()
	if err != nil {
		t.Fatalf("nm on the arm64 build: %v", err)
	}
	for _, want := range []string{"__fn_helper", "__fn_main", "_start"} {
		if !strings.Contains(string(a64Out), want) {
			t.Errorf("arm64 nm output missing %q\n--- got ---\n%s", want, a64Out)
		}
	}
}
