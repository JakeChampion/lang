package e2ecompiler

import (
	"strings"
	"testing"
)

func TestCheckerDriverFault(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   int
		stderr string
		want   string
	}{
		{"clean", 0, "", ""},
		{"diagnostics-emitted", 1, "", ""},
		{"driver-bail-2", 2, "", "bailed with its own failure code 2"},
		{"driver-bail-3", 3, "", "bailed with its own failure code 3"},
		{"signal-death", -1, "", "died on a signal"},
		{"unexpected-code", 99, "", "exited 99"},
		{"stderr-surfaced", 2, "modloader: cannot resolve ./a", "cannot resolve ./a"},
		{"stderr-blank-ignored", 2, "   \n", "failure code 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkerDriverFault(tc.code, []byte(tc.stderr))
			if tc.want == "" {
				if got != "" {
					t.Fatalf("exit %d: want no fault (the driver answered), got %q", tc.code, got)
				}
				return
			}
			if got == "" {
				t.Fatalf("exit %d: want a fault naming %q, got none — a dead driver would read as a clean check", tc.code, tc.want)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("exit %d: fault %q does not name %q", tc.code, got, tc.want)
			}
		})
	}
	if got := checkerDriverFault(2, []byte("   \n")); strings.Contains(got, "stderr:") {
		t.Errorf("blank stderr should be omitted, got %q", got)
	}
}
