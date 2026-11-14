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

// defaultArgsProgram has n functions with a default parameter. Each calls
// the one before it and leaves the default out, so the fill of default
// arguments looks up one callee for each function.
func defaultArgsProgram(n int) string {
	var b strings.Builder
	b.WriteString("function h0(x: i32, d: i32 = 1): i32 { return x + d; }\n")
	for i := 1; i < n; i++ {
		fmt.Fprintf(&b, "function h%d(x: i32, d: i32 = 2): i32 { return h%d(x) + d; }\n", i, i-1)
	}
	fmt.Fprintf(&b, "function main(): i32 { return h%d(1) %% 256; }\n", n-1)
	return b.String()
}

// The time to fill default arguments must grow with the count of calls, not
// with the count of calls times the count of functions (#14578).
func TestSelfHostDefaultArgsFillInLinearTime(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	fastest := func(t *testing.T, src string) time.Duration {
		t.Helper()
		path := filepath.Join(t.TempDir(), "defaults.fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		best := time.Duration(0)
		for range 3 {
			start := time.Now()
			if out, err := exec.Command(cli, "-check", path, stdlib).CombinedOutput(); err != nil {
				t.Fatalf("fern -check: %v\n%s", err, out)
			}
			if took := time.Since(start); best == 0 || took < best {
				best = took
			}
		}
		return best
	}
	// The time above a program with no functions, so the start of the
	// compiler does not hide a square law.
	base := fastest(t, "function main(): i32 { return 0; }\n")
	short := max(fastest(t, defaultArgsProgram(2000))-base, time.Millisecond)
	long := fastest(t, defaultArgsProgram(16000)) - base
	t.Logf("no functions: %v, 2000 functions: +%v, 16000 functions: +%v", base, short, long)
	// Eight times the functions gives 8 for a linear time, 22 for the power
	// 1.5 and 64 for a square law.
	if long > 20*short {
		t.Errorf("2000 functions take %v and 16000 functions take %v: the time grows faster than the count of functions", short, long)
	}
}
