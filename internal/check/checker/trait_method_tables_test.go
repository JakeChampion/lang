package checker

import (
	"errors"
	"github.com/jakechampion/lang/internal/syntax/diag"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func newTableChecker() *checker {
	return &checker{info: &Info{
		Methods:         map[string]string{},
		TraitMethods:    map[string]string{},
		MethodOwners:    map[string][]string{},
		MethodInsts:     map[string][]string{},
		MethodSources:   map[string]string{},
		MethodDeclSites: map[string]ast.Position{},
		Traits:          map[string]*ast.TraitDecl{},
		DirectImports:   map[string]map[string]bool{},
		FuncSigs:        map[string]*ast.FuncType{},
	}}
}

func checkInfo(t *testing.T, src string) *Info {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return info
}

func wantTraitMethod(t *testing.T, info *Info, trait, typeName, name, mangled string) {
	t.Helper()
	key := trait + "." + typeName + "." + name
	got, ok := info.TraitMethods[key]
	if !ok {
		t.Fatalf("TraitMethods[%q] missing; have %v", key, sortedKeys(info.TraitMethods))
	}
	if got != mangled {
		t.Errorf("TraitMethods[%q] = %q, want %q", key, got, mangled)
	}
	if flat := info.Methods[typeName+"."+name]; flat != mangled {
		t.Errorf("Methods[%q] = %q, want %q", typeName+"."+name, flat, mangled)
	}
}

func wantOwners(t *testing.T, info *Info, typeName, name string, want []string) {
	t.Helper()
	key := typeName + "." + name
	got := info.MethodOwners[key]
	if !slices.Equal(got, want) {
		t.Fatalf("MethodOwners[%q] = %v, want %v", key, got, want)
	}
	if !slices.IsSorted(got) {
		t.Errorf("MethodOwners[%q] = %v is not sorted", key, got)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestTraitMethodTablesImplMethod(t *testing.T) {
	info := checkInfo(t, `struct P { x: i32 }
trait Area { function area(self: Self): i32; }
impl Area for P { function area(self: Self): i32 { return self.x; } }
function main(): i32 { let p = P { x: 1 }; return p.area(); }`)

	wantTraitMethod(t, info, "Area", "P", "area", "__method_P_area")
	wantOwners(t, info, "P", "area", []string{"Area"})
}

func TestTraitMethodTablesAssocFunction(t *testing.T) {
	info := checkInfo(t, `trait Default { function default(): Self; }
impl Default for i32 { function default(): Self { return 0; } }
function main(): i32 { return i32.default(); }`)

	wantTraitMethod(t, info, "Default", "i32", "default", "__assoc_i32_default")
	wantOwners(t, info, "i32", "default", []string{"Default"})
}

func TestTraitMethodTablesDerivedMethod(t *testing.T) {
	info := checkInfo(t, `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } }
@derive(Eq)
struct P { x: i32 }
function main(): i32 { let a = P { x: 1 }; let b = P { x: 2 }; if (a.eq(b)) { return 1; } return 0; }`)

	wantTraitMethod(t, info, "Eq", "P", "eq", "__method_P_eq")
	wantOwners(t, info, "P", "eq", []string{"Eq"})
}

func TestTraitMethodTablesInheritedDefault(t *testing.T) {
	info := checkInfo(t, `struct P { x: i32 }
trait Greet {
  function name(self: Self): i32;
  function greet(self: Self): i32 { return self.name() + 1; }
}
impl Greet for P { function name(self: Self): i32 { return self.x; } }
function main(): i32 { let p = P { x: 1 }; return p.greet(); }`)

	wantTraitMethod(t, info, "Greet", "P", "name", "__method_P_name")
	wantTraitMethod(t, info, "Greet", "P", "greet", "__method_P_greet")
	wantOwners(t, info, "P", "greet", []string{"Greet"})
}

func TestTraitMethodTablesInherentMethodHasNoOwner(t *testing.T) {
	info := checkInfo(t, `struct P { x: i32 }
function (p: P) area(): i32 { return p.x; }
function main(): i32 { let p = P { x: 1 }; return p.area(); }`)

	if got := info.Methods["P.area"]; got != "__method_P_area" {
		t.Fatalf(`Methods["P.area"] = %q, want "__method_P_area"`, got)
	}
	if owners, ok := info.MethodOwners["P.area"]; ok {
		t.Errorf(`MethodOwners["P.area"] = %v, want no entry for an inherent method`, owners)
	}
	for k := range info.TraitMethods {
		if len(k) > len(".P.area") && k[len(k)-len(".P.area"):] == ".P.area" {
			t.Errorf("TraitMethods has %q for an inherent method", k)
		}
	}
}

func TestSameMethodFromTwoTraitsIsNotARedeclaration(t *testing.T) {
	info := checkInfo(t, `struct P { x: i32 }
trait A { function go(self: Self): i32; }
trait B { function go(self: Self): i32; }
impl A for P { function go(self: Self): i32 { return 1; } }
impl B for P { function go(self: Self): i32 { return 2; } }
function main(): i32 { let p = P { x: 1 }; return p.x; }`)

	wantOwners(t, info, "P", "go", []string{"A", "B"})
	if got := info.TraitMethods["A.P.go"]; got != "__method_P_go" {
		t.Errorf(`TraitMethods["A.P.go"] = %q, want "__method_P_go"`, got)
	}
	if got := info.TraitMethods["B.P.go"]; got != "__method_P__B__go" {
		t.Errorf(`TraitMethods["B.P.go"] = %q, want "__method_P__B__go"`, got)
	}
}

func TestSameTraitTwiceStillRedeclares(t *testing.T) {
	for name, src := range map[string]string{
		"same trait twice": `struct P { x: i32 }
trait A { function go(self: Self): i32; }
impl A for P { function go(self: Self): i32 { return 1; } }
impl A for P { function go(self: Self): i32 { return 2; } }
function main(): i32 { let p = P { x: 1 }; return p.go(); }`,
		"two inherent": `struct P { x: i32 }
function (p: P) go(): i32 { return 1; }
function (p: P) go(): i32 { return 2; }
function main(): i32 { let p = P { x: 1 }; return p.go(); }`,
		"same assoc fn twice": `struct P { x: i32 }
trait Mk { function make(): Self; }
impl Mk for P { function make(): P { return P { x: 1 }; } }
impl Mk for P { function make(): P { return P { x: 2 }; } }
function main(): i32 { return P.make().x; }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Fatalf("%s: expected E006, got none", name)
		}
		if !strings.Contains(err.Error(), "redeclared") {
			t.Errorf("%s: want a redeclaration diagnostic, got %v", name, err)
		}
	}
}

func TestInherentAndTraitMethodCollide(t *testing.T) {
	for name, src := range map[string]string{
		"inherent declared second": `struct P { x: i32 }
trait A { function go(self: Self): i32; }
impl A for P { function go(self: Self): i32 { return 1; } }
function (p: P) go(): i32 { return 2; }
function main(): i32 { let p = P { x: 1 }; return p.go(); }`,
		"inherent declared first": `struct P { x: i32 }
function (p: P) go(): i32 { return 2; }
trait A { function go(self: Self): i32; }
impl A for P { function go(self: Self): i32 { return 1; } }
function main(): i32 { let p = P { x: 1 }; return p.go(); }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Fatalf("%s: expected E074, got none", name)
		}
		if !strings.Contains(err.Error(), "ambiguous method") || !strings.Contains(err.Error(), "an inherent method") {
			t.Errorf("%s: want the inherent-vs-trait ambiguity, got %v", name, err)
		}
	}
}

func TestAmbiguousCallSiteNamesBothTraitsAndPositions(t *testing.T) {
	err := checkSource(t, `struct P { x: i32 }
trait A { function go(self: Self): i32; }
trait B { function go(self: Self): i32; }
impl A for P { function go(self: Self): i32 { return 1; } }
impl B for P { function go(self: Self): i32 { return 2; } }
function main(): i32 { let p = P { x: 1 }; return p.go(); }`)
	if err == nil {
		t.Fatal("expected E074 for a call with two equally-ranked candidates")
	}
	for _, want := range []string{`ambiguous method "go" on P`, "trait A (declared at 4:16)", "trait B (declared at 5:16)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("E074 text is missing %q:\n%v", want, err)
		}
	}
}

func TestMethodOwnersSorted(t *testing.T) {
	c := newTableChecker()
	for _, trait := range []string{"Zebra", "Mango", "Apple"} {
		mangled := c.mangleMethodName("__method_", "P", "go", trait)
		c.registerMethod("P", "go", trait, mangled, "", ast.Position{})
	}
	wantOwners(t, c.info, "P", "go", []string{"Apple", "Mango", "Zebra"})

	wantTraitMethod(t, c.info, "Zebra", "P", "go", "__method_P_go")
	if got := c.info.TraitMethods["Mango.P.go"]; got != "__method_P__Mango__go" {
		t.Errorf(`TraitMethods["Mango.P.go"] = %q, want "__method_P__Mango__go"`, got)
	}
	if got := c.info.TraitMethods["Apple.P.go"]; got != "__method_P__Apple__go" {
		t.Errorf(`TraitMethods["Apple.P.go"] = %q, want "__method_P__Apple__go"`, got)
	}
}

func TestInstForReceiverPicksMatchingEnumInstantiation(t *testing.T) {
	c := newTableChecker()
	bag := func(arg string) ast.Type {
		return ast.EnumType{Name: "Bag", Args: []ast.Type{ast.StructType{Name: arg}}}
	}
	for _, arg := range []string{"i32", "string"} {
		name := "__method_Bag_count__" + arg
		c.info.FuncSigs[name] = &ast.FuncType{Params: []ast.Type{bag(arg)}}
		c.registerMethod("Bag", "count", "Count", name, "", ast.Position{})
	}
	first := c.info.TraitMethods["Count.Bag.count"]
	if first != "__method_Bag_count__i32" {
		t.Fatalf(`TraitMethods["Count.Bag.count"] = %q, want the first registration`, first)
	}
	for _, arg := range []string{"i32", "string"} {
		if got := c.instForReceiver("Bag", "count", "Count", first, bag(arg)); got != "__method_Bag_count__"+arg {
			t.Errorf("instForReceiver on Bag[%s] = %q, want __method_Bag_count__%s", arg, got, arg)
		}
	}
	if got := c.instForReceiver("Bag", "count", "Count", first, bag("f64")); got != first {
		t.Errorf("instForReceiver on an unregistered instantiation = %q, want the resolved %q", got, first)
	}
}

func TestResolveMethodPrefersNamedTrait(t *testing.T) {
	info := checkInfo(t, `struct P { x: i32 }
trait Area { function area(self: Self): i32; }
impl Area for P { function area(self: Self): i32 { return self.x; } }
function main(): i32 { let p = P { x: 1 }; return p.area(); }`)

	for _, prefer := range [][]string{nil, {"Area"}, {""}, {"Unrelated"}} {
		mangled, owner, ok := info.ResolveMethod("P", "area", prefer)
		if !ok || mangled != "__method_P_area" || owner != "Area" {
			t.Errorf("ResolveMethod(P, area, %v) = %q, %q, %v; want __method_P_area, Area, true", prefer, mangled, owner, ok)
		}
	}
	if _, _, ok := info.ResolveMethod("P", "perimeter", nil); ok {
		t.Error("ResolveMethod resolved a method that does not exist")
	}
}

func TestResolveMethodInherentHasNoOwner(t *testing.T) {
	info := checkInfo(t, `struct P { x: i32 }
function (p: P) area(): i32 { return p.x; }
function main(): i32 { let p = P { x: 1 }; return p.area(); }`)

	mangled, owner, ok := info.ResolveMethod("P", "area", []string{"Area"})
	if !ok || mangled != "__method_P_area" || owner != "" {
		t.Errorf("ResolveMethod(P, area) = %q, %q, %v; want __method_P_area, \"\", true", mangled, owner, ok)
	}
}

func TestResolveMethodPreferenceIgnoresModuleMangling(t *testing.T) {
	c := newTableChecker()
	c.registerMethod("P", "cmp", "cmp__Ord", "__method_P_cmp", "", ast.Position{})

	for _, prefer := range []string{"Ord", "cmp__Ord"} {
		mangled, owner, ok := c.resolveMethod("P", "cmp", []string{prefer})
		if !ok || mangled != "__method_P_cmp" || owner != "cmp__Ord" {
			t.Errorf("resolveMethod(prefer %q) = %q, %q, %v; want __method_P_cmp, cmp__Ord, true", prefer, mangled, owner, ok)
		}
	}
}

func TestResolveMethodOwnerRoundTripsToSameImpl(t *testing.T) {
	c := newTableChecker()
	for _, trait := range []string{"Zebra", "Mango", "Apple"} {
		c.registerMethod("P", "go", trait, c.mangleMethodName("__method_", "P", "go", trait), "", ast.Position{})
	}

	mangled, owner, ok := c.resolveMethod("P", "go", nil)
	if !ok || mangled != "__method_P_go" || owner != "Zebra" {
		t.Fatalf("resolveMethod(P, go, nil) = %q, %q, %v; want __method_P_go, Zebra, true", mangled, owner, ok)
	}
	again, _, ok := c.resolveMethod("P", "go", []string{owner})
	if !ok || again != mangled {
		t.Errorf("re-resolving with owner %q gave %q, want %q", owner, again, mangled)
	}
	for trait, want := range map[string]string{"Mango": "__method_P__Mango__go", "Apple": "__method_P__Apple__go"} {
		got, gotOwner, ok := c.resolveMethod("P", "go", []string{trait})
		if !ok || got != want || gotOwner != trait {
			t.Errorf("resolveMethod(P, go, [%s]) = %q, %q, %v; want %q, %s, true", trait, got, gotOwner, ok, want, trait)
		}
	}
}

func TestAmbiguousDeclReportedAtTheProgramsImpl(t *testing.T) {
	err := checkModuleSource(t, `import "std/i32";
trait Display { function to_string(self: Self): string; }
impl Display for i32 { function to_string(self: Self): string { return "x"; } }
function main(): i32 { return 0; }`)
	var errs diag.Errors
	if !errors.As(err, &errs) || len(errs) == 0 {
		t.Fatalf("expected E074, got %v", err)
	}
	var ce *Error
	if !errors.As(errs[0], &ce) || ce.ErrCode != "E074" {
		t.Fatalf("first error is %v, want E074", errs[0])
	}
	if ce.Pos.Line != 3 || strings.HasPrefix(ce.Path, "stdlib://") {
		t.Errorf("E074 at %s:%s, want line 3 of the program", ce.Path, ce.Pos)
	}
	if !strings.Contains(ce.Msg, "std/i32.fern:") || !strings.Contains(ce.Msg, "main.fern:3:") {
		t.Errorf("E074 does not name both candidates' files: %s", ce.Msg)
	}
}
