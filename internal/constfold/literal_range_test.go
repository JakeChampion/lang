package constfold

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
)

// foldErrors expects Fold to fail and returns each diagnostic as the coded,
// positioned Error the driver renders with a file and a caret.
func foldErrors(t *testing.T, src string) []*Error {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	err = Fold(prog, nil)
	if err == nil {
		t.Fatalf("expected fold error but got none for %q", src)
	}
	es, ok := err.(diag.Errors)
	if !ok {
		t.Fatalf("Fold returned %T, want diag.Errors: %v", err, err)
	}
	var out []*Error
	for _, e := range es {
		fe, ok := e.(*Error)
		if !ok {
			t.Fatalf("%q: diagnostic %T is not a constfold Error: %v", src, e, e)
		}
		out = append(out, fe)
	}
	return out
}

// A const initialiser out of range was reported as an uncoded message at the
// const's 1:1, in different words from the `var` spelling's E047; a negated
// one, or one inside arithmetic, was not reported at all, because the fold
// truncated it to the declared width first; and u64's upper half was refused
// outright, the literal's wrapped Value read as negative. Every literal in a
// const is now judged as written, before folding, by the checker's own rule
// (#8563, #8444).
func TestConstLiteralRangeIsE047AtTheLiteral(t *testing.T) {
	cases := []struct {
		src     string
		want    string
		wantCol int
	}{
		{`const B: i32 = 2147483648;`, "literal 2147483648 does not fit in i32", 16},
		{`const B: i32 = -2147483649;`, "literal -2147483649 does not fit in i32", 17},
		{`const B: u8 = -1;`, "literal -1 does not fit in u8: unsigned types have no negative values", 16},
		{`const B: u32 = 4294967296;`, "literal 4294967296 does not fit in u32", 16},
		{`const B: u8 = 300;`, "literal 300 does not fit in u8", 15},
		{`const B: i64 = 9223372036854775808;`, "literal 9223372036854775808 does not fit in i64", 16},
		{`const B: u64 = -18446744073709551615;`, "literal -18446744073709551615 does not fit in u64: unsigned types have no negative values", 17},
		// An operand takes the const's type, as a var initialiser's does.
		{`const B: i32 = 2147483648 - 1;`, "literal 2147483648 does not fit in i32", 16},
		{`const B: i32 = 1 + -2147483649;`, "literal -2147483649 does not fit in i32", 21},
		// Past u64: refused against the declared type, or the i64 a const
		// with no integer type folds at.
		{`const B: u64 = 18446744073709551616;`, "literal 18446744073709551616 does not fit in u64", 16},
		{`const B: i32 = 18446744073709551616;`, "literal 18446744073709551616 does not fit in i32", 16},
		{`const B = 18446744073709551616;`, "literal 18446744073709551616 does not fit in i64", 11},
		{`const B: boolean = 18446744073709551616 > 1;`, "literal 18446744073709551616 does not fit in i64", 20},
	}
	for _, c := range cases {
		errs := foldErrors(t, c.src+"\nfunction main(): i32 { return 0; }")
		if len(errs) != 1 {
			t.Errorf("%s: %d diagnostics, want exactly one: %v", c.src, len(errs), errs)
			continue
		}
		e := errs[0]
		if e.ErrCode != "E047" {
			t.Errorf("%s: code %q, want E047", c.src, e.ErrCode)
		}
		if e.Pos.Line != 1 || e.Pos.Col != c.wantCol {
			t.Errorf("%s: reported at %d:%d, want the literal at 1:%d", c.src, e.Pos.Line, e.Pos.Col, c.wantCol)
		}
		if e.Msg != c.want {
			t.Errorf("%s: message %q, want %q", c.src, e.Msg, c.want)
		}
	}

	accepted := []string{
		`const B: i32 = 2147483647;`,
		`const B: i32 = -2147483648;`,
		`const B: u8 = 255;`,
		`const B: u32 = -0;`,
		`const B: u64 = 18446744073709551615;`,
		`const B: i64 = -9223372036854775808;`,
		// Arithmetic wraps at the declared width by design (#8444); only a
		// literal that is itself too wide is refused.
		`const B: i32 = 2147483647 + 1;`,
		`const B: i32 = -(2147483647 + 1);`,
	}
	for _, src := range accepted {
		fold(t, src+"\nfunction main(): i32 { return 0; }")
	}
}

// Two bad consts are two diagnostics, each rendered on its own.
func TestConstLiteralRangeReportsEveryConst(t *testing.T) {
	errs := foldErrors(t, "const A: i32 = 2147483648;\nconst B: u8 = 256;\nfunction main(): i32 { return 0; }")
	if len(errs) != 2 || errs[0].Pos.Line != 1 || errs[1].Pos.Line != 2 {
		t.Fatalf("got %v, want one E047 on each line", errs)
	}
}

// A u64 const at the top of the range keeps its bit pattern and the declared
// width: the substituted literal reaches the IR as a u64.
func TestU64ConstUpperHalfSettles(t *testing.T) {
	prog := fold(t, `const B: u64 = 18446744073709551615;
function main(): i32 { return B; }`)
	lit, ok := returnLit(t, prog).(*ast.NumberLit)
	if !ok {
		t.Fatalf("substituted value is %T, want *ast.NumberLit", returnLit(t, prog))
	}
	if uint64(lit.Value) != 18446744073709551615 || !lit.ExceedsI64 || lit.Width != 64 || !lit.IsUnsigned {
		t.Errorf("got Value=%d ExceedsI64=%v Width=%d IsUnsigned=%v, want u64 max settled to u64", lit.Value, lit.ExceedsI64, lit.Width, lit.IsUnsigned)
	}
}

// A usize const settles to the pointer width the way a usize var initialiser
// does; it used to be refused as "declared type usize does not match
// initialiser type i32".
func TestUsizeConstSettlesToPointerWidth(t *testing.T) {
	prog := fold(t, `const B: usize = 5;
function main(): i32 { return B; }`)
	lit, ok := returnLit(t, prog).(*ast.NumberLit)
	if !ok {
		t.Fatalf("substituted value is %T, want *ast.NumberLit", returnLit(t, prog))
	}
	if lit.Width != ast.WidthPtr || !lit.IsUnsigned {
		t.Errorf("got Width=%d IsUnsigned=%v, want the usize stamp", lit.Width, lit.IsUnsigned)
	}
}
