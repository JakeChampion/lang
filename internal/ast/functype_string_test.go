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
