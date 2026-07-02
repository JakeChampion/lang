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
