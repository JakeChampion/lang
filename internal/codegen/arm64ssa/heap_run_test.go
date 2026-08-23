package arm64ssa_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	nativearm64 "github.com/jakechampion/lang/internal/codegen/arm64"
	arm64ssa "github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// Allocation past the 64 KiB the bump heap used to be fixed at: eight 32 KiB
// blocks, each written at both ends and read back. While the heap was a fixed
// .bss buffer with no limit check, the blocks past it were handed out anyway and
// the first store into one killed the program with SIGSEGV (#7325).
func TestArmRunAllocsPastSixtyFourKiB(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	sum := constOp(f, e, 0)
	for i := 0; i < 8; i++ {
		p := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 32<<10))
		storeOp(f, e, p, constOp(f, e, 1), 0)
		storeOp(f, e, p, constOp(f, e, 2), 32760)
		sum = f.AddOp(e, ssa.OpAdd, sum, loadOp(f, e, p, 0))
		sum = f.AddOp(e, ssa.OpAdd, sum, loadOp(f, e, p, 32760))
	}
	f.SetRet(e, sum)
	runMatchesEval(t, f, 12)
}

// An address the program COMPUTES, rather than one a load folds into its
// immediate: five half-gigabyte blocks, each written at both ends through an
// explicit offset add and read back through another. That add is the op the i32
// sign-extend mask used to narrow, so before #7329 every one of these stores
// landed at a truncated negative address and the program died by signal. Only
// two pages per block are touched, so the run costs address space rather than
// memory — which is also why it checks the real run against a hand-computed
// answer rather than against ssa.Eval, whose model heap is a Go slice it would
// have to materialise whole.
func TestArmRunAllocsPastTwoGiB(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	sum := constOp(f, e, 0)
	for i := 0; i < 5; i++ {
		p := f.AddOp(e, ssa.OpAlloc, constOp(f, e, 512<<20))
		for _, off := range []int64{0, (512 << 20) - 8} {
			at := f.AddOp(e, ssa.OpAdd, p, constOp(f, e, off))
			storeOp(f, e, at, constOp(f, e, int64(i)+1), 0)
			sum = f.AddOp(e, ssa.OpAdd, sum, loadOp(f, e, at, 0))
		}
	}
	f.SetRet(e, sum)

	code, stderr := runArmCapturing(t, f, 12)
	if code != 30 {
		t.Errorf("exit = %d (stderr %q), want 30 — 2*(1+2+3+4+5); a negative code "+
			"is a signal death on a truncated address", code, stderr)
	}
}

// Exhausting the arena is a diagnostic and the documented status, not a store
// into unmapped memory: thirty-four half-gigabyte blocks ask for more than the
// 16 GiB reservation holds, so the bump that runs past it aborts. Only each
// block's rc header is written, so the run costs address space rather than
// pages.
func TestArmRunHeapExhaustionAbortsWithArenaStatus(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	var last ssa.Value
	for i := 0; i < 34; i++ {
		last = f.AddOp(e, ssa.OpAlloc, constOp(f, e, 512<<20))
	}
	f.SetRet(e, last)

	code, stderr := runArmCapturing(t, f, 12)
	if code != nativearm64.ExitArenaExhausted {
		t.Errorf("exit = %d, want %d (arena exhausted); a negative code is a signal death, "+
			"which is the wild store this check exists to rule out",
			code, nativearm64.ExitArenaExhausted)
	}
	if !strings.Contains(stderr, nativearm64.MsgArenaExhausted) {
		t.Errorf("stderr = %q, want it to carry %q", stderr, nativearm64.MsgArenaExhausted)
	}
}

// runArmCapturing assembles and runs f under qemu-aarch64 like assembleRunArm,
// returning its exit status and stderr. A death by signal reports as -1.
func runArmCapturing(t *testing.T, f *ssa.Func, numAlloc int) (int, string) {
	t.Helper()
	qemu, err := exec.LookPath("qemu-aarch64")
	if err != nil {
		t.Skip("qemu-aarch64 not available")
	}
	asm, err := arm64ssa.EmitAsm(f, numAlloc)
	if err != nil {
		t.Fatalf("EmitAsm: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "prog")
	if err := os.WriteFile(bin, assembleWX(t, asm), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	cmd := exec.Command(qemu, bin)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if e := cmd.Run(); e != nil {
		var ee *exec.ExitError
		if errors.As(e, &ee) {
			return ee.ExitCode(), errBuf.String()
		}
		t.Fatalf("run: %v", e)
	}
	return 0, errBuf.String()
}
