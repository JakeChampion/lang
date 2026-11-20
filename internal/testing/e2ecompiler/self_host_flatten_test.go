package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

func TestSelfHostFlattenX86_64(t *testing.T) {
	runner := x86_64Runner(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/flatten_check_run.fern")
	binPath := buildSelfHostBinFor(t, dir, "drivers/flatten_check_run.fern", "prog", e2eharness.TargetX86_64Linux)
	cmd := runX86_64Bin(runner, binPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("flatten self-checks: %v\n%s", err, out)
	}
}
