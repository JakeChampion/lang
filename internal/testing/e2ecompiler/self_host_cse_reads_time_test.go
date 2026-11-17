package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// blockReadsProgram has one function that reads n different elements of an
// array in one block, so the merge of equal reads keeps n reads for that block.
func blockReadsProgram(n int) string {
	var b strings.Builder
	b.WriteString("function f(a: i32[]): i32 {\n  let s = 0;\n")
	for i := range n {
		fmt.Fprintf(&b, "  s = s ^ a[%d];\n", i)
	}
	b.WriteString("  return s;\n}\n")
	fmt.Fprintf(&b, "function main(): i32 { return f(__alloc_i32(%d)) %% 256; }\n", max(n, 1))
	return b.String()
}

// The time to merge the equal reads of a block must grow with the count of
// reads, not with its square (#14578).
func TestSelfHostBlockReadsMergeInLinearTime(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	fastest := func(t *testing.T, src string) time.Duration {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "reads.fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		best := time.Duration(0)
		for range 3 {
			start := time.Now()
			if out, err := exec.Command(cli, "-o", filepath.Join(dir, "reads"), path, stdlib).CombinedOutput(); err != nil {
				t.Fatalf("fern -o: %v\n%s", err, out)
			}
			if took := time.Since(start); best == 0 || took < best {
				best = took
			}
		}
		return best
	}
	// The time above a program with no reads, so the start of the compiler
	// does not hide a square law.
	base := fastest(t, blockReadsProgram(0))
	short := max(fastest(t, blockReadsProgram(8000))-base, time.Millisecond)
	long := fastest(t, blockReadsProgram(32000)) - base
	t.Logf("no reads: %v, 8000 reads: +%v, 32000 reads: +%v", base, short, long)
	// Four times the reads gives 4 for a linear time, 8 for the power 1.5
	// and 16 for a square law.
	if long > 8*short {
		t.Errorf("8000 reads take %v and 32000 reads take %v: the time grows faster than the count of reads", short, long)
	}
}
