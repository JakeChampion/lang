package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostAssumeEligibleByteIdenticalX86_64 pins the `-assume-eligible`
// memory optimization (#3457 slice-3 memory blocker): the per-module driver's
// IR-eligibility pre-check (asm_ir.all_eligible_lib_known_view) fully RE-LOWERS
// every function of the module purely to verify it lowers — a second
// whole-module lowering pass on top of the emit's own. On the self-host bump
// arena (no GC) the two passes stack, so the pre-check ~doubles the per-window
// peak (measured on the Go-built driver: irlower window 4.07 GB → 1.81 GB, util
// 3.05 GB → 1.75 GB). `-assume-eligible` skips the pre-check.
//
// The guarantee is BYTE-IDENTITY: skipping a pure verification pass cannot
// change the emitted asm of an eligible module. This test proves that across
// every module of the whole-compiler bootstrap (the only caller of the
// per-module path, and always IR-eligible), then links the flag-emitted units
// into a working compiler and smoke-runs it — so the memory win is free of any
// output or correctness change.
func TestSelfHostAssumeEligibleByteIdenticalX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "ae_driver")
	entry := filepath.Join(dir, "asm_modload_run.fern")

	drive := func(args ...string) (string, error) {
		full := append([]string{entry}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		out, err := cmd.Output()
		return string(out), err
	}

	countOut, err := drive("-per-module-count")
	if err != nil {
		t.Fatalf("-per-module-count: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(countOut))
	if err != nil || n < 10 {
		t.Fatalf("-per-module-count = %q (n=%d), want >= 10", countOut, n)
	}

	needsOut, err := drive("-per-module-needs")
	if err != nil {
		t.Fatalf("-per-module-needs: %v", err)
	}
	var needArgs []string
	for _, ln := range strings.Split(needsOut, "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}

	// Each module, full-module emit, with and without the flag: byte-identical.
	// Link the flag-emitted units into a compiler.
	var objs []string
	entryUnits := 0
	for i := 0; i < n; i++ {
		base := append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)
		checked, err := drive(base...)
		if err != nil || len(checked) == 0 {
			t.Fatalf("module %d emit (checked): %v (%d bytes)", i, err, len(checked))
		}
		assumed, err := drive(append(append([]string{}, base...), "-assume-eligible")...)
		if err != nil || len(assumed) == 0 {
			t.Fatalf("module %d emit (-assume-eligible): %v (%d bytes)", i, err, len(assumed))
		}
		if checked != assumed {
			t.Fatalf("module %d: -assume-eligible output diverges (checked %d bytes, assumed %d bytes, first diff line %d)",
				i, len(checked), len(assumed), firstDiffLine(checked, assumed))
		}
		if strings.Contains(assumed, "\n_start:\n") || strings.HasPrefix(assumed, "_start:\n") {
			entryUnits++
		}
		p := filepath.Join(dir, "ae_unit_"+strconv.Itoa(i)+".s")
		if err := os.WriteFile(p, []byte(assumed), 0o644); err != nil {
			t.Fatalf("write unit %d: %v", i, err)
		}
		objs = append(objs, p)
	}
	if entryUnits != 1 {
		t.Fatalf("expected exactly one entry unit (_start), got %d", entryUnits)
	}
	t.Logf("all %d modules byte-identical with/without -assume-eligible", n)

	compilerBin := filepath.Join(dir, "ae_compiler")
	linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", compilerBin)...)
	if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
		t.Fatalf("link -assume-eligible compiler: %v\n%s", err, lout)
	}
	prog := filepath.Join(dir, "ae_smoke.fern")
	if err := os.WriteFile(prog, []byte("function main(): i32 { return 7; }\n"), 0o644); err != nil {
		t.Fatalf("write smoke prog: %v", err)
	}
	var scmd *exec.Cmd
	if len(runner) == 0 {
		scmd = exec.Command(compilerBin, prog)
	} else {
		scmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), compilerBin), prog)...)
	}
	sout, serr := scmd.Output()
	if serr != nil || !strings.Contains(string(sout), "call __fn_main") {
		t.Fatalf("-assume-eligible compiler smoke run failed: %v (%d bytes asm)", serr, len(sout))
	}
	t.Logf("-assume-eligible-built compiler links and compiles a program (%d bytes asm)", len(sout))
}
