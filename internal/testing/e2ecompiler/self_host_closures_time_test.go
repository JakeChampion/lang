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

// The time to compile closures of one function type must grow with their
// count, not with the square of the count. Each closure has its own
// environment type, and each function that takes the function type reads the
// row of each closure. Four times the closures may take eight times the time.
func TestSelfHostClosuresOfOneTypeCompileInLinearTime(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	const takers = 400
	program := func(closures int) string {
		var b strings.Builder
		for j := range takers {
			fmt.Fprintf(&b, "function g%d(f: (i32) => i32): i32 { return f(%d); }\n", j, j)
		}
		b.WriteString("function main(): i32 {\n  let t = 0;\n")
		for i := range closures {
			fmt.Fprintf(&b, "  let k%d = %d; t = t + g%d((x: i32): i32 => x + k%d);\n", i, i, i%takers, i)
		}
		b.WriteString("  return t % 7;\n}\n")
		return b.String()
	}
	const closures = 400
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	var paths []string
	for i, n := range []int{0, closures, 4 * closures} {
		path := filepath.Join(dir, fmt.Sprintf("closures%d.fern", i))
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
	t.Logf("no closures: %v, %d closures: +%v, %d closures: +%v", best[0], closures, short, 4*closures, long)
	// Four times the count gives 4 for a linear time and 16 for a square law.
	if long > 8*short {
		t.Errorf("%d closures take %v and %d closures take %v: the time grows faster than the count", closures, short, 4*closures, long)
	}
}
