package monomorph_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// `let v = None;` leaves Option's T uninstantiated. The checker accepts it,
// and the re-check monomorphisation runs over a function with a generic call
// must accept it too: it used to read the checker's stamped `Option` back as
// an annotation and fail with "enum Option takes 1 type argument but was
// given 0", at no source position. Found by TestDifferential_CheckerMutants.
func TestUninstantiatedEnumLocalSurvivesRecheck(t *testing.T) {
	for _, src := range []string{
		"function id[T](x: T): T { return x; }\nfunction main(): i32 { let v = None; return id(0); }\n",
		"enum GO[T] { GNil, GHas(T) }\nfunction id[T](x: T): T { return x; }\nfunction main(): i32 { let v = GNil; return id(0); }\n",
	} {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		info, err := checker.Check(prog)
		if err != nil {
			t.Fatalf("check: %v\n%s", err, src)
		}
		if err := monomorph.Run(prog, info); err != nil {
			t.Errorf("monomorph: %v\n%s", err, src)
		}
	}
}
