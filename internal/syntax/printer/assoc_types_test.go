package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

const assocTypeSrc = `trait Holder {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    function get(self: Self): Self::Item { return self.v; }
}
function first[H: Holder](h: H): H::Item { return h.get(); }
function main(): i32 { return 0; }
`

func TestFormatKeepsAssocTypeProjections(t *testing.T) {
	got := formatSrc(t, assocTypeSrc)
	for _, want := range []string{"type Item;", "type Item = i32;", "Self::Item", "H::Item"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted output dropped %q:\n%s", want, got)
		}
	}
}

func TestFormatAssocTypesRoundTrips(t *testing.T) {
	once := formatSrc(t, assocTypeSrc)

	checkProg, err := parser.Parse(once)
	if err != nil {
		t.Fatalf("formatted output does not parse: %v\n%s", err, once)
	}
	if _, err := checker.Check(checkProg); err != nil {
		t.Fatalf("formatted output does not type-check: %v\n%s", err, once)
	}
	if twice := formatSrc(t, once); twice != once {
		t.Errorf("formatting is not idempotent:\nfirst:\n%s\nsecond:\n%s", once, twice)
	}
}
