package checker

import (
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

const clayoutTrait = `trait CLayout { function c_size(): usize; unsafe function c_load(p: usize): Self; unsafe function c_store(self: Self, p: usize): void; }
`

// The offsets follow the C rules: each field at the next multiple of its
// size, and the size of the struct at a multiple of its largest field.
func TestCLayoutOffsets(t *testing.T) {
	cases := []struct {
		fields string
		want   []int
	}{
		{"a: u8, b: i32, c: u8, d: f64, e: u32, f: usize, g: f32", []int{0, 4, 8, 16, 24, 32, 40, 48}},
		{"a: i32, b: i32, c: i32, d: i32", []int{0, 4, 8, 12, 16}},
		{"a: u8, b: u8, c: u8", []int{0, 1, 2, 3}},
		{"a: i64, b: u8", []int{0, 8, 16}},
		{"a: u8, b: f32", []int{0, 4, 8}},
		{"", []int{0}},
	}
	for _, c := range cases {
		prog, err := parser.Parse("struct S { " + c.fields + " }\nfunction main(): i32 { return 0; }")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got := clayoutOffsets(prog.Structs[0]); !slices.Equal(got, c.want) {
			t.Errorf("struct S { %s }: offsets and size %v, want %v", c.fields, got, c.want)
		}
	}
}

func TestDeriveCLayout(t *testing.T) {
	src := clayoutTrait + `@derive(CLayout) struct M { a: u8, b: i32, d: f64, e: u32, f: usize, g: f32, h: i64, k: u64 }
function main(): i32 {
  let p: usize = M.c_size();
  unsafe {
    let m: M = M.c_load(p);
    m.c_store(p);
    M.c_store_b(p, M.c_load_b(p));
    let g: f32 = M.c_load_g(p);
  }
  let off: usize = M.c_offset_g();
  return 0;
}`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("@derive(CLayout) should type-check, got: %v", err)
	}
	bad := []struct{ src, want string }{
		{"@derive(CLayout) struct S { a: string }", "field a has type string, which has no C form"},
		{"@derive(CLayout) struct S { a: boolean }", "field a has type boolean, which has no C form"},
		{"@derive(CLayout) struct S[T] { a: T }", "a struct with a C layout has no type parameters"},
		{"@derive(CLayout) enum S { A(i32) }", "only a struct has a C layout"},
		{"@derive(CLayout) struct S { a: i32 }\nfunction f(): i32 { return S.c_load(0 as usize).a; }", `unsafe function "c_load" is called outside an unsafe block`},
		{"@derive(CLayout) struct S { a: i32 }\nfunction f(): i32 { return S.c_load_a(0 as usize); }", `unsafe function "c_load_a" is called outside an unsafe block`},
	}
	for _, c := range bad {
		err := checkSource(t, clayoutTrait+c.src+"\nfunction main(): i32 { return 0; }")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("got %v, want an error containing %q\nsrc: %s", err, c.want, c.src)
		}
	}
}

// E095 names an associated function as its call spells it, not by its
// mangled name.
func TestUnsafeAssocCallNamesTheFunction(t *testing.T) {
	err := checkSource(t, `struct S { a: i32 }
trait Mk { unsafe function mk(): Self; }
impl Mk for S { unsafe function mk(): S { unsafe { return S { a: 1 }; } } }
function main(): i32 { return S.mk().a; }`)
	if err == nil || !strings.Contains(err.Error(), `unsafe function "mk" is called outside an unsafe block`) {
		t.Fatalf("got %v, want E095 naming \"mk\"", err)
	}
}
