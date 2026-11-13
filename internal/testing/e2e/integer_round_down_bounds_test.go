package e2e

import (
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func roundDownBoundsSource(t *testing.T) (string, []string, []string) {
	t.Helper()
	var source strings.Builder
	source.WriteString(`import "std/i32";
import "std/i64";
@noinline function down32(n: i32, m: i32): i32 { return n.round_down_to(m); }
@noinline function down64(n: i64, m: i64): i64 { return n.round_down_to(m); }
function main(): i32 {
`)
	var want, labels []string
	for _, width := range []uint{32, 64} {
		max := int64((uint64(1) << (width - 1)) - 1)
		min := -max - 1
		inputs := []int64{min, min + 1, min + 2, min + 3, -max/2 - 1, -7, -5, -1, 0, 1, 2, 7, max / 2, max - 1, max}
		multiples := []int64{min, -7, -1, 0, 1, 2, 3, 4, 7, 46341, max / 2, max - 1, max}
		modulus := new(big.Int).Lsh(big.NewInt(1), width)
		literal := func(n int64) string {
			if n == min {
				if width == 64 {
					return "(-9223372036854775807i64 - 1i64)"
				}
				return "(-2147483647 - 1)"
			}
			if width == 64 {
				return fmt.Sprintf("%di64", n)
			}
			return fmt.Sprint(n)
		}
		for _, n := range inputs {
			for _, m := range multiples {
				// Divide in unbounded integers before conversion to the signed width.
				expected := big.NewInt(n)
				if m > 0 {
					expected.Div(expected, big.NewInt(m)).Mul(expected, big.NewInt(m))
				}
				expected.Mod(expected, modulus)
				if expected.Bit(int(width-1)) != 0 {
					expected.Sub(expected, modulus)
				}
				fmt.Fprintf(&source, "    print(down%d(%s, %s));\n", width, literal(n), literal(m))
				want = append(want, expected.String())
				labels = append(labels, fmt.Sprintf("i%d n=%d m=%d", width, n, m))
			}
		}
	}
	source.WriteString("    return 0;\n}\n")
	entry := filepath.Join(t.TempDir(), "round_down.fern")
	if err := os.WriteFile(entry, []byte(source.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return entry, want, labels
}

func checkRoundDownBounds(t *testing.T, output string, want, labels []string) {
	t.Helper()
	got := strings.Fields(output)
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d:\n%s", len(got), len(want), output)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: got %s, want %s", labels[i], got[i], want[i])
		}
	}
	t.Logf("checked %d signed round_down_to cases", len(want))
}

func roundDownBoundsCompiled(t *testing.T, target string, runner []string) {
	t.Helper()
	entry, want, labels := roundDownBoundsSource(t)
	bin := e2eharness.CompileSelfHostFile(t, target, entry, nil)
	args := append(append([]string{}, runner...), bin)
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("round_down_to: %v\n%s", err, out)
	}
	checkRoundDownBounds(t, string(out), want, labels)
}

func TestIntegerRoundDownBoundsInterp(t *testing.T) {
	entry, want, labels := roundDownBoundsSource(t)
	out, code := e2eharness.InterpVerdict(t, buildLangBinForInterp(t), entry)
	if code != 0 {
		t.Fatalf("round_down_to exit %d:\n%s", code, out)
	}
	checkRoundDownBounds(t, string(out), want, labels)
}

func TestIntegerRoundDownBoundsX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	roundDownBoundsCompiled(t, e2eharness.TargetX86_64Linux, runner)
}

func TestIntegerRoundDownBoundsArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	var runner []string
	if qemu != "" {
		runner = []string{qemu}
	}
	roundDownBoundsCompiled(t, e2eharness.TargetArm64Linux, runner)
}

func TestIntegerRoundDownBoundsWasm(t *testing.T) {
	roundDownBoundsCompiled(t, e2eharness.TargetWasm32Wasi, []string{e2eharness.Wasmtime(t), "run"})
}
