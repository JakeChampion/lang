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

// The time to check a chain must grow with its length, not with the square
// of its length. Four times the length may take ten times the time.
func TestSelfHostChainChecksInLinearTime(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	// Eight functions hold the chain, so the time of the chains is well
	// above the noise of the machine.
	inMain := func(body string) string {
		var b strings.Builder
		for i := range 8 {
			fmt.Fprintf(&b, "function chain%d(): i32 { let y: i32 = 3; %s return 0; }\n", i, body)
		}
		b.WriteString("function main(): i32 { return chain0(); }\n")
		return b.String()
	}
	// fastest checks each program once per round and keeps the best time of
	// each. The rounds take the programs in turn, so a period of load on the
	// machine slows each program and not only one of them.
	fastest := func(t *testing.T, srcs ...string) []time.Duration {
		t.Helper()
		paths := make([]string, len(srcs))
		for i, src := range srcs {
			paths[i] = filepath.Join(t.TempDir(), "chain.fern")
			if err := os.WriteFile(paths[i], []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		best := make([]time.Duration, len(srcs))
		for range 5 {
			for i, path := range paths {
				start := time.Now()
				if out, err := exec.Command(cli, "-check", path, stdlib).CombinedOutput(); err != nil {
					t.Fatalf("fern -check: %v\n%s", err, out)
				}
				if took := time.Since(start); best[i] == 0 || took < best[i] {
					best[i] = took
				}
			}
		}
		return best
	}
	for _, tc := range []struct {
		name  string
		links int
		chain func(n int) string
	}{
		{"integer literals", 1000, func(n int) string {
			return inMain("let x: i32 = 1" + strings.Repeat(" + 1", n) + ";")
		}},
		{"integer literals beside a name", 1000, func(n int) string {
			return inMain("let x: i32 = y" + strings.Repeat(" + 1", n) + ";")
		}},
		{"integer literals into i64", 1000, func(n int) string {
			return inMain("let x: i64 = 1" + strings.Repeat(" * 1", n) + ";")
		}},
		{"names", 1000, func(n int) string {
			return inMain("let x: i32 = y" + strings.Repeat(" + y", n) + ";")
		}},
		{"negations", 1000, func(n int) string {
			return inMain("let x: i32 = " + strings.Repeat("-", n) + "1;")
		}},
		{"negations of a name", 1000, func(n int) string {
			return inMain("let x: i32 = " + strings.Repeat("-", n) + "y;")
		}},
		{"parentheses", 600, func(n int) string {
			return inMain("let x: i32 = " + strings.Repeat("(", n) + "y" + strings.Repeat(")", n) + ";")
		}},
		{"casts", 1000, func(n int) string {
			return inMain("let x: i32 = y" + strings.Repeat(" as i32", n) + ";")
		}},
		{"right-nested integer literals", 300, func(n int) string {
			return inMain("let x: i32 = " + strings.Repeat("1 + (", n) + "1" + strings.Repeat(")", n) + ";")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The time above a program with no chain, so the start of the
			// compiler does not hide a square law.
			times := fastest(t, tc.chain(0), tc.chain(tc.links), tc.chain(4*tc.links))
			base := times[0]
			short := max(times[1]-base, time.Millisecond)
			long := times[2] - base
			t.Logf("no links: %v, %d links: +%v, %d links: +%v", base, tc.links, short, 4*tc.links, long)
			// Four times the length gives 4 for a linear time, 8 for the
			// power 1.5 and 16 for a square law.
			if long > 8*short {
				t.Errorf("%d links take %v and %d links take %v: the time grows faster than the length", tc.links, short, 4*tc.links, long)
			}
		})
	}
}
