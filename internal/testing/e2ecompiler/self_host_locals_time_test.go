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

// The time to compile a function must grow with the count of its locals, not
// with the square of the count. Four times the locals may take eight times
// the time.
func TestSelfHostLocalsCompileInLinearTime(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	program := func(locals int) string {
		var b strings.Builder
		b.WriteString("function main(): i32 {\n  let v = 0;\n")
		for i := range locals {
			fmt.Fprintf(&b, "  let v%d = %d;\n", i, i%97)
		}
		b.WriteString("  return v;\n}\n")
		return b.String()
	}
	const locals = 8000
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	var paths []string
	for i, n := range []int{0, locals, 4 * locals} {
		path := filepath.Join(dir, fmt.Sprintf("locals%d.fern", i))
		if err := os.WriteFile(path, []byte(program(n)), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	// Each round compiles the three programs in turn, so a period of load on
	// the machine slows each of them and not only one.
	best := make([]time.Duration, len(paths))
	for range 3 {
		for i, path := range paths {
			if took := cpuTime(t, cli, "-target", "x86-64-linux", "-o", out, path, stdlib); best[i] == 0 || took < best[i] {
				best[i] = took
			}
		}
	}
	short := max(best[1]-best[0], time.Millisecond)
	long := best[2] - best[0]
	t.Logf("no locals: %v, %d locals: +%v, %d locals: +%v", best[0], locals, short, 4*locals, long)
	// Four times the count gives 4 for a linear time and 16 for a square law.
	if long > 8*short {
		t.Errorf("%d locals take %v and %d locals take %v: the time grows faster than the count", locals, short, 4*locals, long)
	}
}
