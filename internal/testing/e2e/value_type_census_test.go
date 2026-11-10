package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValueTypeCleanupCensus(t *testing.T) {
	_, runner := x86_64Tooling(t)
	bin, err := buildTracedX86_64(t, filepath.Join(conformanceCases, "value_cleanup_keeps_cow_aliases", "main.fern"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runSplit(t, runX86_64Bin(runner, bin))
	if exit != 0 || stdout != "7\n9\n7\n2\n" {
		t.Fatalf("value cleanup: exit %d, stdout %q, stderr %s", exit, stdout, stderr)
	}
	if !strings.Contains(stderr, "rctrace a ") {
		t.Fatal("value cleanup has no allocation trace")
	}
	n, top, err := pairRcTrace(stderr)
	if err != nil || n != 0 {
		t.Fatalf("value cleanup: %d unpaired allocations: %s (%v)", n, top, err)
	}
}
