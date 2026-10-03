package checker

import (
	"strings"
	"testing"
)

const traitParamDecls = `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Sq): i32 { return self.s * self.s; } }
struct Rect { w: i32, h: i32 }
impl Shape for Rect { function area(self: Rect): i32 { return self.w * self.h; } }
`

// A parameter typed by a trait is an anonymous type parameter bounded by it,
// one per parameter, so two such parameters take two different types.
func TestTraitParamIsAnonymousGeneric(t *testing.T) {
	src := traitParamDecls + `function total(a: Shape, b: Shape): i32 { return a.area() + b.area(); }
function main(): i32 { return total(Sq { s: 3 }, Rect { w: 2, h: 5 }); }`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("a trait-typed parameter should check as a generic: %v", err)
	}
}

// A type parameter the function writes itself keeps its meaning even when it
// shares a trait's name, and the anonymous one steps around a written name.
func TestTraitParamLeavesWrittenTypeParamsAlone(t *testing.T) {
	src := traitParamDecls + `function same[Shape](x: Shape): Shape { return x; }
function pick[T_a](a: Shape, b: T_a): T_a { let n: i32 = a.area(); return b; }
function main(): i32 { let s: string = same("x"); let k: i32 = pick(Sq { s: 1 }, 7); return k; }`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("written type parameters should be untouched: %v", err)
	}
}

func TestTraitParamErrors(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"argument without the impl", traitParamDecls + `function total(a: Shape): i32 { return a.area(); }
function main(): i32 { return total(5); }`, "type argument T_a = i32 does not implement trait Shape required by total"},
		{"trait as a return type", traitParamDecls + `function make(): Shape { return Sq { s: 1 }; }
function main(): i32 { return 0; }`, "unknown type \"Shape\" (`Shape` is a trait: a parameter of trait type makes the function generic over it; anywhere else write `dyn Shape`)"},
		{"trait as a local's type", traitParamDecls + `function main(): i32 { let x: Shape = Sq { s: 2 }; return 0; }`, "`Shape` is a trait"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, tc.src)
			if err == nil {
				t.Fatalf("want an error containing %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want an error containing %q, got:\n%v", tc.want, err)
			}
		})
	}
}
