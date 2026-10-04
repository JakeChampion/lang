package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// A generic function named as a value is instantiated from the function type
// it is wanted at, on the interpreter and every native target. The self-host
// twin is TestSelfHostGenericFnValue.
func TestGenericFnValue(t *testing.T) {
	main := e2eharness.WriteGenericFnValueProgram(t)
	t.Run("interp", func(t *testing.T) {
		out, err := exec.Command(buildLangBinForInterp(t), "-interp", main).CombinedOutput()
		if err != nil {
			t.Fatalf("interp: %v\n%s", err, out)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		if out, code := runFixtureX86_64(t, main, ""); code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, out)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if out, code := runFixtureArm64(t, main, ""); code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, out)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if out, code := runFixtureWasm(t, main, ""); code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, out)
		}
	})
	// The type arguments a generic callee's other arguments pin are bound
	// after the checker typed the value, so their bounds are monomorph's to
	// check, as a call's are; `-check` runs it.
	t.Run("bound unmet through a generic callee", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.fern")
		src := `trait Shape { function area(self: Self): i32; }
function measure[T: Shape](s: T): i32 { return s.area(); }
function twice[A](f: (A) => i32, v: A): i32 { return f(v) * 2; }
function main(): i32 { return twice(measure, 5); }
`
		if err := os.WriteFile(bad, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(buildLangBinForInterp(t), "-check", bad).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "does not implement trait Shape") {
			t.Fatalf("want -check to refuse the unmet bound, got err=%v:\n%s", err, out)
		}
	})
}
