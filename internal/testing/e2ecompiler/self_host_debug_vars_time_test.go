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

// manyLocalsProgram has one function with n local variables, so -g gives n
// rows of variable locations for that function.
func manyLocalsProgram(n int) string {
	var b strings.Builder
	b.WriteString("function f(a: i32): i32 {\n  let v0 = a ^ 1;\n")
	for i := 1; i < n; i++ {
		fmt.Fprintf(&b, "  let v%d = v%d + %d;\n", i, i-1, i%7)
	}
	fmt.Fprintf(&b, "  return v%d;\n}\nfunction main(): i32 { return f(3) %% 256; }\n", n-1)
	return b.String()
}

// The rows of variable locations must not take a time that grows with the
// square of the count of variables (#14578). Before the fix, -g added 90% to
// the compile of this program, and 3.3 s.
func TestSelfHostDebugVarsInLinearTime(t *testing.T) {
	target := e2eharness.HostSelfHostTarget()
	if target == "" {
		t.Skip("no native self-host target")
	}
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "locals.fern")
	if err := os.WriteFile(path, []byte(manyLocalsProgram(16000)), 0o644); err != nil {
		t.Fatal(err)
	}
	fastest := func(flags ...string) time.Duration {
		args := append(flags, "-target", target, "-o", filepath.Join(dir, "locals"), path, stdlib)
		best := time.Duration(0)
		for range 2 {
			start := time.Now()
			if out, err := exec.Command(cli, args...).CombinedOutput(); err != nil {
				t.Fatalf("fern %v: %v\n%s", flags, err, out)
			}
			if took := time.Since(start); best == 0 || took < best {
				best = took
			}
		}
		return best
	}
	plain := fastest()
	added := fastest("-g") - plain
	t.Logf("16000 variables: %v, and -g adds %v", plain, added)
	// Both limits, so a faster compile without -g does not make the test fail.
	if added > plain*2/5 && added > 1500*time.Millisecond {
		t.Errorf("-g adds %v to a compile of %v: the rows of variable locations take too long", added, plain)
	}
}
