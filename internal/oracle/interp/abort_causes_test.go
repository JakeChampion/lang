package interp

import (
	"os"
	"strings"
	"testing"
)

// TestAbortCausesMatchTheCompiler pins each cause line to the one
// compiler/aborts.fern gives a compiled program for the same stop.
func TestAbortCausesMatchTheCompiler(t *testing.T) {
	src, err := os.ReadFile("../../../compiler/aborts.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, cause := range []string{CauseArrOOB, CauseStrOOB, CauseSliceRange, CauseAllocSize} {
		if !strings.Contains(string(src), "return \""+cause+"\";") {
			t.Errorf("compiler/aborts.fern has no cause line %q", cause)
		}
	}
}
