package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnderInferredGenericStructLitIsADiagnostic(t *testing.T) {
	bin := buildLangBinForCheck(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "stack.fern")
	if err := os.WriteFile(src, []byte(`struct Stack[T] { items: T[] }
function main(): i32 { let s = Stack { items: [] }; return 0; }
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
			if !strings.Contains(all, "^") {
				t.Errorf("output has no caret:\n%s", all)
			}
		})
	}
}
