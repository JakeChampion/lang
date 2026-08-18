package e2eselfhost

import (
	"strings"
	"testing"
)

// The checker differentials turn a driver's stdout into a code SET, where EMPTY
// is a legitimate answer meaning "no diagnostics" — so a driver that bailed or
// crashed before printing reads as a clean check, and the differential blames
// the checker for a disagreement it did not cause.
//
// Tested through the classifier rather than a driver that deliberately dies,
// which would pin the harness to one way of crashing.
func TestCheckerDriverFault(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   int
		stderr string
		want   string // "" = answered; otherwise a substring the message must carry
	}{
		{"clean", 0, "", ""},
		{"diagnostics-emitted", 1, "", ""},
		// The driver's own bail paths (checker_modload_run.fern main): 2 on the
		// read/argument path, 3 on the fallthrough. Both used to read as "no
		// diagnostics".
		{"driver-bail-2", 2, "", "bailed with its own failure code 2"},
		{"driver-bail-3", 3, "", "bailed with its own failure code 3"},
		// A signal death surfaces as a negative code from ExitCode().
		{"signal-death", -1, "", "died on a signal"},
		// An unexpected positive code is still a failure to answer, and must say
		// which code so the next reader does not have to guess.
		{"unexpected-code", 99, "", "exited 99"},
		// stderr is the driver's own account of what went wrong; it must reach
		// the failure message rather than being dropped with the error.
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
	// The blank-stderr case must not append an empty "stderr:" section.
	if got := checkerDriverFault(2, []byte("   \n")); strings.Contains(got, "stderr:") {
		t.Errorf("blank stderr should be omitted, got %q", got)
	}
}
