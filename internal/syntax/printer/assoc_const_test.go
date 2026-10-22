package printer

import (
	"strings"
	"testing"
)

const assocConstSrc = `trait Lanes {
    const LANES: i32;
    const NAME: string = "lanes";
    function width(self: Self): i32;
}
struct V2 { a: f64 }
impl Lanes for V2 {
    const LANES: i32 = 2;
    function width(self: Self): i32 { return Self.LANES; }
}
function chunk[T: Lanes](n: i32): i32 { return n / T.LANES + f64.LANES; }
function main(): i32 { return 0; }
`

// An associated constant survives formatting in both bodies, and its reads
// stay field accesses on the type name.
func TestFormatKeepsAssocConsts(t *testing.T) {
	got := formatSrc(t, assocConstSrc)
	for _, want := range []string{"  const LANES: i32;\n", "  const NAME: string = \"lanes\";\n", "  const LANES: i32 = 2;\n", "Self.LANES", "T.LANES", "f64.LANES"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "function LANES") {
		t.Errorf("a const was formatted as a function:\n%s", got)
	}
	again := formatSrc(t, got)
	if again != got {
		t.Errorf("formatting is not a fixpoint:\n%s\n---\n%s", got, again)
	}
}
