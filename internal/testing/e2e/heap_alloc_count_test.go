package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// --- The allocation count (#9596) ---------------------------------
//
// `__heap_alloc_count()` reports how many blocks the allocator has handed
// out, the freelist-pop path and the bump path alike. It is the half
// `__heap_bump_bytes()` cannot see — a pop hands out a block without moving
// the cursor — and so the only observable that can tell a steady state which
// allocates nothing from one which allocates and recycles.
//
// The runtime behaviour is pinned by the conformance case
// `alloc_count_sees_recycling`. What is left here is the COST side of the
// contract: the counter lives in the allocator, and a module that never reads
// it must carry neither the counter nor the tick — the same byte-identity
// promise FERN_LEAKCHECK and FERN_SANITIZE make, measured by the same cheap
// proxy (no symbol in the text).

const allocCountReaderSrc = `function main(): i32 {
	return (__heap_alloc_count() as i32);
}
`

const allocCountSilentSrc = `function main(): i32 {
	let xs: i32[] = [1, 2, 3];
	return xs.len();
}
`

// emitAllocCount emits src for one target with the census off, so the only
// thing that can put the counter in the text is the program reading it.
func emitAllocCount(t *testing.T, target, src string) string {
	t.Helper()
	srcPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	return e2eharness.EmitAsmWithSelfHost(t, e2eharness.SelfHostCLI(t), target, srcPath)
}

var allocCountTargets = []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux}

// A program that never reads the count is shaped as it was before the
// observable existed: no counter, no tick.
func TestHeapAllocCountUnreadEmitsNoCounter(t *testing.T) {
	for _, target := range allocCountTargets {
		if asm := emitAllocCount(t, target, allocCountSilentSrc); strings.Contains(asm, "__fern_alloc_count") {
			t.Errorf("%s: a program that never reads the count carries the counter anyway", target)
		}
	}
}

// Reading it puts the counter and the allocator's tick in, and nothing else:
// the exit report stays behind FERN_LEAKCHECK, so a measuring program does not
// start printing a census line.
func TestHeapAllocCountReadEmitsCounterButNoReport(t *testing.T) {
	for _, target := range allocCountTargets {
		asm := emitAllocCount(t, target, allocCountReaderSrc)
		if !strings.Contains(asm, "__fern_alloc_count") {
			t.Errorf("%s: a program reading the count emits no counter", target)
		}
		if strings.Contains(asm, "leakcheck: allocs=") {
			t.Errorf("%s: reading the count dragged in the census report", target)
		}
	}
}
