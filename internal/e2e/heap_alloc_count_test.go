package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
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
// `alloc_count_sees_recycling`, across both compilers and all three default
// backends. What is left to the flat emitters here is the COST side of the
// contract: the counter is the leak census's, and a module that never reads
// it must carry neither the counter nor the ticks — the same byte-identity
// promise FERN_LEAKCHECK and -sanitize already make, measured by the same
// cheap proxy (no symbol in the text).

const allocCountReaderSrc = `function main(): i32 {
	return (__heap_alloc_count() as i32);
}
`

const allocCountSilentSrc = `function main(): i32 {
	var xs: i32[] = [1, 2, 3];
	return xs.len();
}
`

// emitAllocCount compiles src for one flat backend with every census flag off,
// so the only thing that can put a counter in the text is the program reading
// it.
func emitAllocCount(t *testing.T, backend, src string) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}

	prevLc := ast.LeakCheckEnabled
	t.Cleanup(func() { ast.LeakCheckEnabled = prevLc })
	// An ambient FERN_LEAKCHECK in the developer's environment would emit the
	// counters on its own and make the silent leg lie.
	ast.LeakCheckEnabled = false

	var asm string
	var emitErr error
	if backend == "arm64" {
		asm, emitErr = arm64codegen.Emit(prog, info)
	} else {
		asm, emitErr = x86_64.Emit(prog, info)
	}
	if emitErr != nil {
		t.Fatalf("%s emit: %v", backend, emitErr)
	}
	return asm
}

// A program that never reads the count is shaped as it was before the
// observable existed: no counter, no tick.
func TestHeapAllocCountUnreadEmitsNoCounter(t *testing.T) {
	for _, backend := range []string{"x86_64", "arm64"} {
		asm := emitAllocCount(t, backend, allocCountSilentSrc)
		if strings.Contains(asm, "__fern_lc_alloc_count") {
			t.Errorf("%s: a program that never reads the count carries the census counter anyway", backend)
		}
		if strings.Contains(asm, "__fern_heap_alloc_count") {
			t.Errorf("%s: a program that never reads the count carries the reader anyway", backend)
		}
	}
}

// Reading it puts the counter, its reader, and the allocator's tick in — and
// nothing else: the exit report stays behind FERN_LEAKCHECK, so a measuring
// program does not start printing a census line.
func TestHeapAllocCountReadEmitsCounterButNoReport(t *testing.T) {
	for _, backend := range []string{"x86_64", "arm64"} {
		asm := emitAllocCount(t, backend, allocCountReaderSrc)
		for _, want := range []string{"__fern_lc_alloc_count", "__fern_heap_alloc_count"} {
			if !strings.Contains(asm, want) {
				t.Errorf("%s: a program reading the count emits no %s", backend, want)
			}
		}
		if strings.Contains(asm, "leakcheck: allocs=") {
			t.Errorf("%s: reading the count dragged in the census report", backend)
		}
	}
}
