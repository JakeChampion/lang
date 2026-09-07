package ast

import "testing"

// TestFuncTypeStringNilParamDoesNotPanic guards the regression where
// formatting a FuncType with a nil parameter (or result) type panicked
// with a nil-pointer dereference inside p.String(). This happens for a
// `use x <- f()` whose callback parameter type the checker couldn't pin
// (inferUseParam bails, leaving the synthesised callback's first param
// nil); the type is then formatted while building an E038 diagnostic, so
// a panic there masked the real error with `%!s(PANIC=...)`.
func TestFuncTypeStringNilParamDoesNotPanic(t *testing.T) {
	cases := []struct {
		name string
		ft   *FuncType
		want string
	}{
		{
			name: "nil param",
			ft:   &FuncType{Params: []Type{nil}, Result: NumberType{Width: 32, Signed: true}},
			want: "(<unknown>) => i32",
		},
		{
			name: "nil result",
			ft:   &FuncType{Params: []Type{NumberType{Width: 32, Signed: true}}, Result: nil},
			want: "(i32) => <unknown>",
		},
		{
			name: "nil param among real ones",
			ft:   &FuncType{Params: []Type{NumberType{Width: 32, Signed: true}, nil}, Result: BoolType{}},
			want: "(i32, <unknown>) => boolean",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.ft.String()
			if got != tc.want {
				t.Errorf("FuncType.String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFuncElemStringKeepsParens guards the rendering of an array / slice of
// function values. Without the grouping parens, `((i32) => i32)[]` prints as
// `(i32) => i32[]` — the spelling of a function returning an array of i32 —
// so the two distinct types render identically and neither re-parses to
// itself. The func element is the one spelling whose own grammar swallows the
// `[]` suffix; every other element round-trips unparenthesised.
func TestFuncElemStringKeepsParens(t *testing.T) {
	fn := &FuncType{Params: []Type{NumberType{Width: 32, Signed: true}}, Result: NumberType{Width: 32, Signed: true}}
	cases := []struct {
		name string
		ty   Type
		want string
	}{
		{name: "array of functions", ty: ArrayType{Elem: fn}, want: "((i32) => i32)[]"},
		{name: "slice of functions", ty: SliceType{Elem: fn}, want: "[(i32) => i32]"},
		{name: "array of strings stays bare", ty: ArrayType{Elem: StringType{}}, want: "string[]"},
		{name: "array of tuples stays bare", ty: ArrayType{Elem: TupleType{Elems: []Type{NumberType{Width: 32, Signed: true}, StringType{}}}}, want: "(i32, string)[]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ty.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}
