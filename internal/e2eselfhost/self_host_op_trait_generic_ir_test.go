package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Arithmetic operator overloading over a trait-bounded TYPE PARAMETER on the
// self-host IR path (#2706): `a + b` / `(a + b) * (a - b)` / unary `-a` where the
// operands have type `T` and `T`'s bound provides the op's trait method
// (`num.Num` = Add+Sub+Mul+Div, `num.Neg`). The native checker desugars these to
// `a.add(b)` / `a.neg()` resolved through the bound; the self-host reaches the
// same result by monomorphising the bounded `T` to its concrete instantiation,
// so a generic numeric body with operators lowers on the IR path and matches the
// interpreter. (Nested operands like `(a-b)*b` exercise the post-check rewrite
// recursing into the desugared call's receiver.)
var opTraitGenericIRCases = []struct {
	name string
	src  string
}{
	// nested (a+b)+c over T: Num.
	{"sum3", `import "std/num";
function sum3[T: num.Num](a: T, b: T, c: T): T { return a + b + c; }
function main(): i32 { return sum3(10, 20, 12); }`},
	// nested both sides: (a+b)*(a-b) = a^2 - b^2.
	{"diff-of-squares", `import "std/num";
function dsq[T: num.Num](a: T, b: T): T { return (a + b) * (a - b); }
function main(): i32 { return dsq(5, 3); }`},
	// unary minus over T: Neg.
	{"unary-neg", `import "std/num";
function ng[T: num.Neg](a: T): T { return -a; }
function main(): i32 { return ng(0 - 5); }`},
	// generic accumulate over an array with the `+` operator in a loop.
	{"accumulate", `import "std/num";
function tot[T: num.Num](xs: T[], z: T): T { var acc: T = z; for x in xs { acc = acc + x; } return acc; }
function main(): i32 { var xs: i32[] = [3, 4, 5, 6]; return tot(xs, 0); }`},
}

func TestSelfHostOpTraitGenericIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range opTraitGenericIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "optgen_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "optgen_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
