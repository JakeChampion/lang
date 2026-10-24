package constfold

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestEntryConstDoesNotReachImportedBodies(t *testing.T) {
	for _, src := range []string{
		"import \"std/dns\";\nconst A: i32 = 1;\nfunction main(): i32 { print(A.to_string()); return 0; }\n",
		"import \"std/dns\";\nconst A = 1;\nfunction main(): i32 { print(A.to_string()); return 0; }\n",
	} {
		prog, _, err := modload.LoadSource(src)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if err := Fold(prog, nil); err != nil {
			t.Fatalf("fold: %v", err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Errorf("%s: check: %v", src, err)
		}
	}
}
