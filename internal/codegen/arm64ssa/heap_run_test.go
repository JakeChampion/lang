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

// Exhausting the arena is a diagnostic and the documented status, not a store
// into unmapped memory: five half-gigabyte blocks ask for more than the
// reservation holds, so the bump that runs past it aborts. The blocks are never
// written, so the run costs address space rather than pages.
func TestArmRunHeapExhaustionAbortsWithArenaStatus(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	var last ssa.Value
	for i := 0; i < 5; i++ {
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
