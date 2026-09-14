package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
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

// An associated-type projection survives formatting. `formatType` had no
// `ProjType` arm, so the switch fell through to "" and `-fmt` wrote
// `function get(self: Self): ;` — output that does not parse. The corpus
// properties would have caught it, but no corpus file uses associated types.
func TestFormatKeepsAssocTypeProjections(t *testing.T) {
	got := formatSrc(t, assocTypeSrc)
	for _, want := range []string{"type Item;", "type Item = i32;", "Self::Item", "H::Item"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted output dropped %q:\n%s", want, got)
		}
	}
}

// The corpus properties, stated for this shape: the formatted output still
// type-checks, and formatting is idempotent.
func TestFormatAssocTypesRoundTrips(t *testing.T) {
	once := formatSrc(t, assocTypeSrc)

	// Check on its own program: it injects the builtin struct / enum decls
	// into the AST, so the copy it was handed can no longer be formatted.
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
