package interp

import (
	"os"
	"strings"
	"testing"
)

func TestAbortCausesMatchTheCompiler(t *testing.T) {
	src, err := os.ReadFile("../../../compiler/aborts.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, cause := range []string{CauseArrOOB, CauseStrOOB, CauseSliceRange, CauseAllocSize, CauseRecursion} {
		if !strings.Contains(string(src), "return \""+cause+"\";") {
			t.Errorf("compiler/aborts.fern has no cause line %q", cause)
		}
	}
}
