package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func nestedParens(n int) string {
	return "function main(): i32 { return " + strings.Repeat("(", n) + "0" + strings.Repeat(")", n) + "; }\n"
}

func nestedBlocks(n int) string {
	return "function main(): i32 " + strings.Repeat("{ ", n) + "return 0; " + strings.Repeat("} ", n) + "\n"
}

func nativeAcceptsNesting(shape func(int) string, n int) bool {
	_, err := parser.Parse(shape(n))
	return err == nil
}

func nativeNestingLimit(t *testing.T, shape func(int) string) int {
	t.Helper()
	lo, hi := 1, 1<<14
	if nativeAcceptsNesting(shape, hi) {
		t.Fatalf("native still accepts %d levels — raise the bisection ceiling", hi)
	}
	if !nativeAcceptsNesting(shape, lo) {
		t.Fatalf("native rejects %d level(s) — the bound is not where this test assumes", lo)
	}
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if nativeAcceptsNesting(shape, mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

func TestSelfHostDeepNestingMatchesNativeAndDoesNotCrash(t *testing.T) {
	requireSelfHostDiffLeg(t)
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the self-host CLI driver runs only natively (argv paths)")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	compile := func(t *testing.T, shape func(int) string, n int) (string, bool) {
		t.Helper()
		work := t.TempDir()
		src := filepath.Join(work, "main.fern")
		if err := os.WriteFile(src, []byte(shape(n)), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-o", filepath.Join(work, "out.s"), src)
		cmd.Env = append(os.Environ(), "FERN_STDLIB_ROOT="+stdlibRoot)
		r := runSelfHostBin(cmd, "")
		if !r.exited {
			t.Fatalf("%d levels: self-host died on a signal (%s) instead of reporting — "+
				"the parser is recursing past its bound again", n, r.state)
		}
		if r.timedOut {
			t.Fatalf("%d levels: self-host hung", n)
		}
		return r.stdout + r.stderr, r.exit == 0
	}

	for _, sh := range []struct {
		name  string
		shape func(int) string
	}{{"parens", nestedParens}, {"blocks", nestedBlocks}} {
		t.Run(sh.name, func(t *testing.T) {
			limit := nativeNestingLimit(t, sh.shape)
			t.Logf("native accepts %d levels of %s nesting and refuses %d", limit, sh.name, limit+1)

			t.Run("at the limit it still compiles", func(t *testing.T) {
				if out, ok := compile(t, sh.shape, limit); !ok {
					t.Errorf("native accepts %d levels but the self-host refused them:\n%s", limit, out)
				}
			})

			t.Run("one past the limit it reports P005", func(t *testing.T) {
				out, ok := compile(t, sh.shape, limit+1)
				if ok {
					t.Fatalf("native refuses %d levels but the self-host accepted them", limit+1)
				}
				if !strings.Contains(out, "P005") {
					t.Errorf("want the P005 nesting diagnostic native reports, got:\n%s", out)
				}
			})

			for _, n := range []int{20000, 60000} {
				t.Run("no crash at "+strconv.Itoa(n), func(t *testing.T) {
					out, ok := compile(t, sh.shape, n)
					if ok {
						t.Fatalf("%d levels compiled clean; expected a refusal", n)
					}
					if !strings.Contains(out, "P005") {
						t.Errorf("want P005 at %d levels, got:\n%s", n, out)
					}
				})
			}
		})
	}
}
