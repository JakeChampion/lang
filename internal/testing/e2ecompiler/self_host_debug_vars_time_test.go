package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// assignmentsProgram has one function that assigns one variable n times, so
// the variable has n values and each one gets a row of debug information.
func assignmentsProgram(n int) string {
	var b strings.Builder
	b.WriteString("function f(x: i32): i32 {\n  let t = x;\n")
	for i := range n {
		fmt.Fprintf(&b, "  t = (t * 31 + %d) %% 65521;\n", i)
	}
	b.WriteString("  return t;\n}\n")
	b.WriteString("function main(): i32 { return f(args().len()) % 256; }\n")
	return b.String()
}

// The time to write the variable rows of a -g build must grow with the count
// of values of a variable, not with its square (#14578).
func TestSelfHostDebugVarsInLinearTime(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	fastest := func(t *testing.T, src string) time.Duration {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "vars.fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		best := time.Duration(0)
		for range 3 {
			if took := cpuTime(t, cli, "-g", "-o", filepath.Join(dir, "vars"), path, stdlib); best == 0 || took < best {
				best = took
			}
		}
		return best
	}
	// The time above a program with no assignments, so the start of the
	// compiler does not hide a square law.
	base := fastest(t, assignmentsProgram(0))
	short := max(fastest(t, assignmentsProgram(4000))-base, time.Millisecond)
	long := fastest(t, assignmentsProgram(16000)) - base
	t.Logf("no assignments: %v, 4000 assignments: +%v, 16000 assignments: +%v", base, short, long)
	// Four times the assignments gives 4 for a linear time, 8 for the power
	// 1.5 and 16 for a square law.
	if long > 8*short {
		t.Errorf("4000 assignments take %v and 16000 assignments take %v: the time grows faster than the count of assignments", short, long)
	}
}
