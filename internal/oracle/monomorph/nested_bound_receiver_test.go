package monomorph_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestNestedReceiverRetainsItsImplBound(t *testing.T) {
	const source = `trait Value { function get(self: Self): i32; }
struct Box[T] { value: T }
impl[T] Value for Box[T] {
    function get(self: Box[T]): i32 { return 7; }
}
struct Wrapper[T] { value: T }
impl[T: Value] Value for Wrapper[T] {
    function get(self: Wrapper[T]): i32 { return self.value.get(); }
}
function main(): i32 {
    let r = Wrapper { value: Wrapper { value: Box { value: 7 } } };
    return r.get();
}`
	prog, err := parser.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("nested generic receiver lost its bound: %v", err)
	}
}
