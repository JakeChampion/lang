package x86_64ssa

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	nativex86_64 "github.com/jakechampion/lang/internal/codegen/x86_64"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
	"github.com/jakechampion/lang/internal/ssa"
)

// Allocation past the 64 KiB the bump heap used to be fixed at: four 32 KiB
// blocks, each written at both ends and read back. While the heap was a fixed
// .bss buffer with no limit check, the blocks past it were handed out anyway
// and the first store into one killed the program (#7330, the x86-64 half of
// #7325).
func TestRunAllocsPastSixtyFourKiB(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	one := constOp(f, e, 1)
	two := constOp(f, e, 2)
	sum := constOp(f, e, 0)
	for i := 0; i < 4; i++ {
		p := allocOp(f, e, 32<<10)
		storeOp(f, e, p, one, 0)
		storeOp(f, e, p, two, 32760)
		sum = f.AddOp(e, ssa.OpAdd, sum, loadOp(f, e, p, 0))
		sum = f.AddOp(e, ssa.OpAdd, sum, loadOp(f, e, p, 32760))
	}
	f.SetRet(e, sum)
	runMatchesEval(t, f, 10)
}

// Exhausting the arena is a diagnostic and the documented status, not a store
// into unmapped memory: five half-gigabyte blocks ask for more than the
// reservation holds, so the bump that runs past it aborts. The blocks are never
// written, so the run costs address space rather than pages.
func TestRunHeapExhaustionAbortsWithArenaStatus(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	var last ssa.Value
	for i := 0; i < 5; i++ {
		last = allocOp(f, e, 512<<20)
	}
	f.SetRet(e, last)

	code, stderr := runCapturing(t, f, 10)
	if code != nativex86_64.ExitArenaExhausted {
		t.Errorf("exit = %d, want %d (arena exhausted); a negative code is a signal death, "+
			"which is the wild store this check exists to rule out",
			code, nativex86_64.ExitArenaExhausted)
	}
	if !strings.Contains(stderr, nativex86_64.MsgArenaExhausted) {
		t.Errorf("stderr = %q, want it to carry %q", stderr, nativex86_64.MsgArenaExhausted)
	}
}

// runCapturing assembles and runs f like assembleRun, returning its exit status
// and stderr. A death by signal reports as -1.
func runCapturing(t *testing.T, f *ssa.Func, numAlloc int) (int, string) {
	t.Helper()
	if runtime.GOARCH != "amd64" || runtime.GOOS != "linux" {
		t.Skipf("native x86-64 run needs amd64/linux, have %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	asm, err := EmitAsm(f, numAlloc)
	if err != nil {
		t.Fatalf("EmitAsm: %v", err)
	}
	text, rodata, err := nativex86.AssembleProgram(asm, nativeelf.TextVAddr)
	if err != nil {
		t.Fatalf("AssembleProgram: %v\n--- asm ---\n%s", err, asm)
	}
	bin := filepath.Join(t.TempDir(), "prog")
	if err := os.WriteFile(bin, nativeelf.StaticExecutableDataX86(text, rodata), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	cmd := exec.Command(bin)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if e := cmd.Run(); e != nil {
		var ee *exec.ExitError
		if !errors.As(e, &ee) {
			t.Fatalf("run: %v", e)
		}
	}
	return cmd.ProcessState.ExitCode(), errBuf.String()
}
