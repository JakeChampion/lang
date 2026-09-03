// #8194: a boolean that is not a comparison's result reaches an `if` or a
// loop guard through OpNot, which the #4378 compare-and-branch fusion does
// not cover — the run of OpNots has no comparison in front of it. Both
// native backends now fold that run into the branch's own zero test, which
// means the polarity is computed by parity of the run rather than emitted.
// A parity slip is a silent miscompile that inverts one branch, so this runs
// the matrix — boolean source x negation depth x consumer — through both
// backends and pins each answer against the interpreter.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// notBranchMatrix builds a program whose main returns 0 only if every
// combination agrees with the answer written beside it. Each `f<N>` takes
// its boolean from a call — opaque to the folder that would otherwise
// constant-fold the whole thing away — and consumes it after 0 to 3
// negations, through an `if`, an `if/else` and a `while` guard.
func notBranchMatrix() string {
	var b strings.Builder
	b.WriteString(`@noinline function src(x: i32): boolean { return x > 2; }
`)
	var checks []string
	id := 0
	for _, nots := range []int{1, 2, 3} {
		neg := strings.Repeat("!", nots)
		// `if (NOT b)`: the then-arm runs when the negated value holds.
		fmt.Fprintf(&b, "@noinline function fi%d(x: i32): i32 { var b: boolean = src(x); if (%sb) { return 1; } return 0; }\n", id, neg)
		// `if (NOT b) else`: both arms present, so the else-label is real.
		fmt.Fprintf(&b, "@noinline function fe%d(x: i32): i32 { var b: boolean = src(x); if (%sb) { return 1; } else { return 0; } }\n", id, neg)
		// `while (NOT b)`: the guard is the OpBrIf-shaped consumer; the
		// body clears the condition so the loop runs at most once.
		fmt.Fprintf(&b, "@noinline function fw%d(x: i32): i32 { var b: boolean = src(x); var n: i32 = 0; while (%sb) { n = n + 1; b = %s; } return n; }\n",
			id, neg, map[bool]string{true: "true", false: "false"}[nots%2 == 1])
		// src(1) is false, src(5) is true; an odd run of negations makes
		// the effective condition the opposite of the source.
		for _, in := range []struct {
			arg  int
			srcT bool
		}{{1, false}, {5, true}} {
			want := 0
			if (nots%2 == 1) != in.srcT {
				want = 1
			}
			for _, fn := range []string{"fi", "fe", "fw"} {
				checks = append(checks, fmt.Sprintf("%s%d(%d) != %d", fn, id, in.arg, want))
			}
		}
		id++
	}
	b.WriteString("function main(): i32 {\n")
	for i, c := range checks {
		fmt.Fprintf(&b, "  if (%s) { return %d; }\n", c, i+1)
	}
	b.WriteString("  return 0;\n}\n")
	return b.String()
}

func TestNotBranchFusionMatrix(t *testing.T) {
	src := notBranchMatrix()
	bin := buildFernCLI(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "notbranch.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	// The interpreter is the oracle: it never sees the fusion, so a
	// non-zero exit here means the matrix itself is wrong, not the fold.
	interp := exec.Command(bin, "-interp", p)
	_ = interp.Run()
	if code := interp.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("interp exit = %d, want 0 — check %d of the matrix disagrees with the interpreter\n%s", code, code, src)
	}

	t.Run("x86-64", func(t *testing.T) {
		qemu := x86QemuOrEmpty(t)
		out := filepath.Join(dir, "notbranch.x86")
		if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", out, p).CombinedOutput(); err != nil {
			t.Fatalf("x86-64 build: %v\n%s", err, o)
		}
		cmd := runX86Bin(qemu, out)
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Errorf("x86-64 exit = %d, want 0 — check %d of the matrix took the wrong branch", code, code)
		}
	})

	t.Run("arm64", func(t *testing.T) {
		qemu := arm64QemuOrEmpty(t)
		out := filepath.Join(dir, "notbranch.arm64")
		if o, err := exec.Command(bin, "-target", "arm64-linux", "-o", out, p).CombinedOutput(); err != nil {
			t.Fatalf("arm64 build: %v\n%s", err, o)
		}
		cmd := exec.Command(qemu, out)
		if qemu == "" {
			cmd = exec.Command(out)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Errorf("arm64 exit = %d, want 0 — check %d of the matrix took the wrong branch", code, code)
		}
	})
}
