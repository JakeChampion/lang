package constfold

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
)

// An entry const sharing a name with an imported module's variant must not
// reach that module's bodies: std/dns builds its `A` record with `A(...)`,
// which folding the entry's `const A` into turned into a call of an i32 (#11143).
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
