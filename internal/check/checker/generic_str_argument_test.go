package checker

import "testing"

func TestExplicitStrArgumentAcceptsStringLiteral(t *testing.T) {
	if err := checkSource(t, `function identity[T](x: T): T { return x; }
function main(): i32 { let s: str = identity[str]("abc"); return s.len(); }`); err != nil {
		t.Fatal(err)
	}
}
