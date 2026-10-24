package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const allocCountReaderSrc = `function main(): i32 {
	return (__heap_alloc_count() as i32);
}
`

const allocCountSilentSrc = `function main(): i32 {
	let xs: i32[] = [1, 2, 3];
	return xs.len();
}
`

func emitAllocCount(t *testing.T, target, src string) string {
	t.Helper()
	srcPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	return e2eharness.EmitAsmWithSelfHost(t, e2eharness.SelfHostCLI(t), target, srcPath)
}

var allocCountTargets = []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux}

func TestHeapAllocCountUnreadEmitsNoCounter(t *testing.T) {
	for _, target := range allocCountTargets {
		if asm := emitAllocCount(t, target, allocCountSilentSrc); strings.Contains(asm, "__fern_alloc_count") {
			t.Errorf("%s: a program that never reads the count carries the counter anyway", target)
		}
	}
}

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
