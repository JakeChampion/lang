package ast

import "testing"

func TestOwnershipString(t *testing.T) {
	cases := []struct {
		o    Ownership
		want string
	}{
		{Owned, "owned"},
		{Borrowed, "borrowed"},
		{View, "view"},
		{Static, "static"},
	}
	for _, c := range cases {
		if got := c.o.String(); got != c.want {
			t.Errorf("Ownership(%d).String() = %q, want %q", int(c.o), got, c.want)
		}
	}
}

func TestOwnershipNeedsRC(t *testing.T) {
	// Only Owned participates in reference counting; a Borrowed reference is
	// the caller's to release, and View / Static must never be freed.
	if !Owned.NeedsRC() {
		t.Error("Owned.NeedsRC() = false, want true")
	}
	for _, o := range []Ownership{Borrowed, View, Static} {
		if o.NeedsRC() {
			t.Errorf("%s.NeedsRC() = true, want false", o)
		}
	}
}

func TestStructuralOwnership(t *testing.T) {
	// SliceType is the one structural view; a slice value aliases its parent
	// array's storage, so it must never free that buffer.
	if got := StructuralOwnership(SliceType{Elem: NumberType{}}); got != View {
		t.Errorf("StructuralOwnership([i32]) = %s, want view", got)
	}

	// A borrow-handle carries its borrow bit in the type; an owning handle
	// (and any other pointer-shaped or scalar type) defaults to Owned.
	if got := StructuralOwnership(HandleType{Resource: "R", Borrowed: true}); got != Borrowed {
		t.Errorf("StructuralOwnership(borrow R) = %s, want borrowed", got)
	}
	if got := StructuralOwnership(HandleType{Resource: "R"}); got != Owned {
		t.Errorf("StructuralOwnership(own R) = %s, want owned", got)
	}

	// Owned by default: the owned array `T[]` (distinct from the `[T]` view
	// above), strings, structs, tuples, and scalars.
	ownedByDefault := []Type{
		ArrayType{Elem: NumberType{}},
		StringType{},
		TupleType{Elems: []Type{NumberType{}, StringType{}}},
		StructType{Name: "Point"},
		NumberType{},
		BoolType{},
	}
	for _, ty := range ownedByDefault {
		if got := StructuralOwnership(ty); got != Owned {
			t.Errorf("StructuralOwnership(%s) = %s, want owned", ty.String(), got)
		}
	}
}

func TestExprResultOwnership(t *testing.T) {
	// A string literal is immortal (.rodata), even though its type is the
	// structurally-Owned StringType.
	if got := ExprResultOwnership(&StringLit{Value: "hi"}, StringType{}); got != Static {
		t.Errorf("ExprResultOwnership(string literal) = %s, want static", got)
	}

	// A string slice copies into a fresh owned string (__str_slice); an array
	// slice aliases its parent's storage (a view).
	strSlice := &SliceExpr{Source: &StringLit{Value: "hello"}, IsString: true}
	if got := ExprResultOwnership(strSlice, StringType{}); got != Owned {
		t.Errorf("ExprResultOwnership(string slice) = %s, want owned", got)
	}
	arrSlice := &SliceExpr{Source: &Ident{Name: "xs"}, IsString: false}
	if got := ExprResultOwnership(arrSlice, SliceType{Elem: NumberType{}}); got != View {
		t.Errorf("ExprResultOwnership(array slice) = %s, want view", got)
	}

	// Fresh constructions and scalar literals are Owned.
	owned := []Expr{
		&ArrayLit{},
		&StructLit{TypeName: "Point"},
		&TupleLit{},
		&MapLit{},
		&NumberLit{},
		&BoolLit{},
		&Call{},
	}
	for _, e := range owned {
		if got := ExprResultOwnership(e, StringType{}); got != Owned {
			t.Errorf("ExprResultOwnership(%T) = %s, want owned", e, got)
		}
	}

	// An unclassified expression (a bare identifier) falls back to the type's
	// structural ownership — a slice-typed local is a View, a string local is
	// Owned by default (its binding-precise ownership is a later slice).
	if got := ExprResultOwnership(&Ident{Name: "s"}, SliceType{Elem: NumberType{}}); got != View {
		t.Errorf("ExprResultOwnership(slice ident) = %s, want view", got)
	}
	if got := ExprResultOwnership(&Ident{Name: "s"}, StringType{}); got != Owned {
		t.Errorf("ExprResultOwnership(string ident) = %s, want owned", got)
	}
}
