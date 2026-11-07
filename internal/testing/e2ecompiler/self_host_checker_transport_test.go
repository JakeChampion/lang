package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckerCodesOutput(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("shell is required for driver transport controls")
	}
	for _, tc := range []struct {
		name, script, output, errorText string
	}{
		{"accepted", "exit 0", "", ""},
		{"diagnosed", "printf 'E003\\twrong type\\n'; exit 1", "E003\twrong type\n", ""},
		{"parser verdict", "printf 'P002\\tfloat out of range\\n'; exit 1", "P002\tfloat out of range\n", ""},
		{"empty failure", "exit 1", "", "exit status 1"},
		{"malformed verdict", "printf 'driver crashed\\n'; exit 1", "driver crashed\n", "exit status 1"},
		{"unexpected exit", "exit 2", "", "exit status 2"},
		{"diagnostic then failure", "printf 'E003\\twrong type\\n'; exit 2", "E003\twrong type\n", "exit status 2"},
		{"stderr retained", "printf 'loader failed\\n' >&2; exit 2", "", "loader failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := checkerCodesOutput(shell, tc.script)
			if out != tc.output {
				t.Fatalf("stdout = %q, want %q", out, tc.output)
			}
			if tc.errorText == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.errorText) {
				t.Fatalf("error = %v, want %q", err, tc.errorText)
			}
		})
	}
	t.Run("cannot start", func(t *testing.T) {
		out, err := checkerCodesOutput(filepath.Join(t.TempDir(), "missing-driver"), "")
		if out != "" || err == nil {
			t.Fatalf("stdout = %q, error = %v; want an execution failure", out, err)
		}
	})
}
