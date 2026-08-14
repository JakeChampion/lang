package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An under-inferred generic struct literal must reach the user as a
// diagnostic, not as an internal-invariant failure (#6813). `[]` cannot
// pin `T`, so unification bound it to itself; the "inferred" arg named a
// parameter that exists nowhere at the construction site, and monomorph
// mangled it into a struct nobody declared. What the user saw was
// `monomorph: re-check failed (compiler bug): … unknown struct type
// "Stack"` plus a second line leaking the mangled name `Stack__T` — no
// code for `fern -explain`, no excerpt, no caret.
//
// The golden in testdata/diag_golden/E040_struct_inference.golden pins the
// rendered shape; this pins that the internal-failure wording cannot come
// back for it, through the real CLI rather than a library call.
func TestUnderInferredGenericStructLitIsADiagnostic(t *testing.T) {
	bin := buildLangBinForCheck(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "stack.fern")
	if err := os.WriteFile(src, []byte(`struct Stack[T] { items: T[] }
function main(): i32 { var s = Stack { items: [] }; return 0; }
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	for _, mode := range []string{"-check", "-interp"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(bin, mode, src)
			var out, errb bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errb
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 1 {
				t.Errorf("exit = %d, want 1", code)
			}
			all := out.String() + errb.String()
			if !strings.Contains(all, "error[E040]") {
				t.Errorf("output has no error[E040]:\n%s", all)
			}
			for _, banned := range []string{"compiler bug", "Stack__T", "monomorph"} {
				if strings.Contains(all, banned) {
					t.Errorf("output still contains %q:\n%s", banned, all)
				}
			}
			// The caret line proves it rendered as a real diagnostic
			// rather than bare `at 2:32` coordinates.
			if !strings.Contains(all, "^") {
				t.Errorf("output has no caret:\n%s", all)
			}
		})
	}
}
