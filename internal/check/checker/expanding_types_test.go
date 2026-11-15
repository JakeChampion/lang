package checker

import (
	"strings"
	"testing"
)

func TestGenericTypeWithNoLastInstanceIsE107(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"struct", "struct Nest[T] { head: T, tail: Nest[Nest[T]] }\n", `generic struct "Nest" has no last instance: field "tail" gives the type parameter "T" a larger type each time`},
		{"array-argument", "struct Deep[T] { head: T, tail: Option[Deep[T[]]] }\n", `generic struct "Deep" has no last instance: field "tail"`},
		{"enum", "enum Nest[T] { Leaf(T), More(Nest[T[]]) }\n", `generic enum "Nest" has no last instance: variant "More" gives the type parameter "T" a larger type each time`},
		{"through-a-second-struct", "struct A[T] { head: T, b: Option[B[T[]]] }\nstruct B[T] { a: Option[A[T]] }\n", `generic struct "A" has no last instance: field "b"`},
		{"in-a-function-type", "struct Call[T] { f: (Call[T[]]) => i32 }\n", `generic struct "Call" has no last instance: field "f"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkErrors(t, tc.src+"function main(): i32 { return 0; }\n")
			if len(errs) != 1 || errs[0].ErrCode != "E107" || !strings.Contains(errs[0].Msg, tc.want) {
				t.Fatalf("got %v, want one E107 with %q", errs, tc.want)
			}
			if errs[0].Pos.Line != 1 || errs[0].Pos.Col != 1 {
				t.Errorf("reported at %d:%d, want the declaration at 1:1", errs[0].Pos.Line, errs[0].Pos.Col)
			}
		})
	}
}

func TestRecursiveGenericTypeThatDoesNotGrowIsAccepted(t *testing.T) {
	for name, src := range map[string]string{
		"same-argument":      "struct List[T] { head: T, tail: Option[List[T]] }\n",
		"exchanged":          "struct Pair[A, B] { a: A, b: B, next: Option[Pair[B, A]] }\n",
		"fixed-argument":     "struct Tree[T] { v: T, names: Option[Tree[string]] }\n",
		"grows-with-no-loop": "struct Box[T] { v: T }\nstruct Rows[T] { rows: Box[T[]] }\n",
		"enum":               "enum Chain[T] { End, Link(T, Chain[T]) }\n",
	} {
		t.Run(name, func(t *testing.T) {
			if errs := checkErrors(t, src+"function main(): i32 { return 0; }\n"); len(errs) != 0 {
				t.Fatalf("got %v, want no error", errs)
			}
		})
	}
}
