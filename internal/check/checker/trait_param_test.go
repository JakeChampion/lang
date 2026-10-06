package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/parser"
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

// A generic trait with its arguments is the trait too: the parser spells
// `Sink[i32]` as an EnumType, and the bound keeps the arguments.
func TestTraitParamWithTypeArguments(t *testing.T) {
	src := `trait Sink[T] { function put(self: Self, v: T): i32; }
struct Acc { n: i32 }
impl Sink[i32] for Acc { function put(self: Acc, v: i32): i32 { return self.n + v; } }
function feed(s: Sink[i32]): i32 { return s.put(4); }
function main(): i32 { return feed(Acc { n: 3 }); }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("a parameter typed by a generic trait should check as a generic: %v", err)
	}
	feed := findDecl(prog, "feed")
	if got := feed.TypeParams; len(got) != 1 || got[0] != "T_s" {
		t.Fatalf("feed type params = %v, want [T_s]", got)
	}
	if got := feed.BoundArgs["T_s"]; len(got) != 1 || len(got[0]) != 1 {
		t.Fatalf("feed bound args = %v, want the one argument of Sink[i32]", got)
	}
}

func TestTraitParamErrors(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"argument without the impl", traitParamDecls + `function total(a: Shape): i32 { return a.area(); }
function main(): i32 { return total(5); }`, "type argument T_a = i32 does not implement trait Shape required by total"},
		{"an array of a trait is not the trait", traitParamDecls + `function total(xs: Shape[]): i32 { return 7; }
function main(): i32 { return 0; }`, "unknown type \"Shape\" (`Shape` is a trait"},
		{"an undeclared type beside a trait-typed parameter", traitParamDecls + `function total(a: Shape, b: Wibble): i32 { return a.area(); }
function main(): i32 { return 0; }`, "unknown type \"Wibble\""},
		// A trait's methods are not generic, so an impl's method keeps the
		// parameter as written and is told what to write instead, rather than
		// failing conformance against a generated name.
		{"a trait-typed parameter in an impl method", traitParamDecls + `trait Cmp { function eqto(self: Self, o: Shape): boolean; }
impl Cmp for Sq { function eqto(self: Sq, o: Shape): boolean { return self.s == o.area(); } }
function main(): i32 { return 0; }`, "unknown type \"Shape\" (`Shape` is a trait"},
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
			if strings.Contains(err.Error(), "wrong signature") {
				t.Errorf("a generated type parameter must not reach a conformance report:\n%v", err)
			}
		})
	}
}

// The hint names the spelling the reader can write, not the mangled one an
// imported module's own trait carries in a type position.
func TestTraitParamHintDemanglesImportedTrait(t *testing.T) {
	prog, _, err := modload.LoadSource(`import "core/cmp";
function make(): cmp.Display { return 1; }
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, err = Check(prog)
	if err == nil {
		t.Fatal("a trait as a return type should be E064")
	}
	for _, want := range []string{"unknown type \"cmp.Display\"", "write `dyn cmp.Display`"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in:\n%v", want, err)
		}
	}
}
