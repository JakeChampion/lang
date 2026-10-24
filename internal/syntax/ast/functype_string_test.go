package ast

import "testing"

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
