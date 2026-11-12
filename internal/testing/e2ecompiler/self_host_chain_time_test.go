package e2ecompiler

import (
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
	inMain := func(body string) string {
		return "function main(): i32 { let y: i32 = 3; " + body + " return 0; }\n"
	}
	fastest := func(t *testing.T, src string) time.Duration {
		t.Helper()
		path := filepath.Join(t.TempDir(), "chain.fern")
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
		{"casts", 1000, func(n int) string {
			return inMain("let x: i32 = y" + strings.Repeat(" as i32", n) + ";")
		}},
		{"right-nested integer literals", 300, func(n int) string {
			return inMain("let x: i32 = " + strings.Repeat("1 + (", n) + "1" + strings.Repeat(")", n) + ";")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			short := fastest(t, tc.chain(tc.links))
			long := fastest(t, tc.chain(4*tc.links))
			t.Logf("%d links: %v, %d links: %v", tc.links, short, 4*tc.links, long)
			if long > 10*short {
				t.Errorf("%d links take %v and %d links take %v: the time grows faster than the length", tc.links, short, 4*tc.links, long)
			}
		})
	}
}
