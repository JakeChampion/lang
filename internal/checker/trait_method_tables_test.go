package checker

import (
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// Info.TraitMethods / Info.MethodOwners are the trait-aware index beside
// the flat Info.Methods namespace: they record WHICH trait provided each
// hoisted method, so a method name offered by two traits for one type
// stays addressable. Every path that produces an impl method has to stamp
// `FuncDecl.ImplTrait` for that to hold — the parser's impl desugar, the
// checker's trait-default synthesis, and `@derive` expansion alike.

// newTableChecker is a checker with just the method tables wired up, for
// driving registerMethod / resolveMethod directly.
func newTableChecker() *checker {
	return &checker{info: &Info{
		Methods:         map[string]string{},
		TraitMethods:    map[string]string{},
		MethodOwners:    map[string][]string{},
		MethodSources:   map[string]string{},
		MethodDeclSites: map[string]ast.Position{},
		Traits:          map[string]*ast.TraitDecl{},
		DirectImports:   map[string]map[string]bool{},
	}}
}

// checkInfo parses + checks src and returns the resulting Info, failing
// the test on any diagnostic.
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

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// An ordinary `impl Trait for Type` method registers under the trait and
// keeps the historical flat mangling.
func TestTraitMethodTablesImplMethod(t *testing.T) {
	info := checkInfo(t, `struct P { x: i32 }
trait Area { function area(self: Self): i32; }
impl Area for P { function area(self: Self): i32 { return self.x; } }
function main(): i32 { var p = P { x: 1 }; return p.area(); }`)

	wantTraitMethod(t, info, "Area", "P", "area", "__method_P_area")
	wantOwners(t, info, "P", "area", []string{"Area"})
}

// An associated function (`impl Default for i32 { function default() }`)
// hoists to `__assoc_…` and is indexed the same way.
func TestTraitMethodTablesAssocFunction(t *testing.T) {
	info := checkInfo(t, `trait Default { function default(): Self; }
impl Default for i32 { function default(): Self { return 0; } }
function main(): i32 { return i32.default(); }`)

	wantTraitMethod(t, info, "Default", "i32", "default", "__assoc_i32_default")
	wantOwners(t, info, "i32", "default", []string{"Default"})
}

// A @derive-d method is synthesised by the checker, not the parser, so it
// carries its own ImplTrait stamp.
func TestTraitMethodTablesDerivedMethod(t *testing.T) {
	info := checkInfo(t, `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } }
@derive(Eq)
struct P { x: i32 }
function main(): i32 { var a = P { x: 1 }; var b = P { x: 2 }; if (a.eq(b)) { return 1; } return 0; }`)

	wantTraitMethod(t, info, "Eq", "P", "eq", "__method_P_eq")
	wantOwners(t, info, "P", "eq", []string{"Eq"})
}

// A trait DEFAULT method the impl does not override is materialised by
// synthesizeTraitDefaults, and must be attributed to the trait too.
func TestTraitMethodTablesInheritedDefault(t *testing.T) {
	info := checkInfo(t, `struct P { x: i32 }
trait Greet {
  function name(self: Self): i32;
  function greet(self: Self): i32 { return self.name() + 1; }
}
impl Greet for P { function name(self: Self): i32 { return self.x; } }
function main(): i32 { var p = P { x: 1 }; return p.greet(); }`)

	wantTraitMethod(t, info, "Greet", "P", "name", "__method_P_name")
	wantTraitMethod(t, info, "Greet", "P", "greet", "__method_P_greet")
	wantOwners(t, info, "P", "greet", []string{"Greet"})
}

// An inherent method belongs to no trait: it registers in the flat
// namespace only, and contributes NO MethodOwners entry — a missing key
// is what "not provided by a trait" looks like.
func TestTraitMethodTablesInherentMethodHasNoOwner(t *testing.T) {
	info := checkInfo(t, `struct P { x: i32 }
function (p: P) area(): i32 { return p.x; }
function main(): i32 { var p = P { x: 1 }; return p.area(); }`)

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

// An inherent `impl Type { … }` block is not a trait impl either: its
// methods and associated functions go in the flat namespace with no owner.
func TestTraitMethodTablesInherentImplBlockHasNoOwner(t *testing.T) {
	info := checkInfo(t, `struct P { x: i32 }
impl P {
  function origin(): P { return P { x: 0 }; }
  function get(self: Self): i32 { return self.x; }
}
function main(): i32 { var p = P.origin(); return p.get(); }`)

	for _, key := range []string{"P.origin", "P.get"} {
		if _, ok := info.Methods[key]; !ok {
			t.Fatalf("Methods[%q] missing", key)
		}
		if owners, ok := info.MethodOwners[key]; ok {
			t.Errorf("MethodOwners[%q] = %v, want no entry for an inherent impl", key, owners)
		}
	}
}

// Two DIFFERENT traits providing one method name for one type is no
// longer a redeclaration: both register, each addressable under its own
// trait. What the call site does with them is E074's business.
func TestSameMethodFromTwoTraitsIsNotARedeclaration(t *testing.T) {
	info := checkInfo(t, `struct P { x: i32 }
trait A { function go(self: Self): i32; }
trait B { function go(self: Self): i32; }
impl A for P { function go(self: Self): i32 { return 1; } }
impl B for P { function go(self: Self): i32 { return 2; } }
function main(): i32 { var p = P { x: 1 }; return p.x; }`)

	wantOwners(t, info, "P", "go", []string{"A", "B"})
	if got := info.TraitMethods["A.P.go"]; got != "__method_P_go" {
		t.Errorf(`TraitMethods["A.P.go"] = %q, want "__method_P_go"`, got)
	}
	if got := info.TraitMethods["B.P.go"]; got != "__method_P__B__go" {
		t.Errorf(`TraitMethods["B.P.go"] = %q, want "__method_P__B__go"`, got)
	}
}

// The SAME trait providing one method name twice for one type is still a
// plain redeclaration, and so are two inherent declarations.
func TestSameTraitTwiceStillRedeclares(t *testing.T) {
	for name, src := range map[string]string{
		"same trait twice": `struct P { x: i32 }
trait A { function go(self: Self): i32; }
impl A for P { function go(self: Self): i32 { return 1; } }
impl A for P { function go(self: Self): i32 { return 2; } }
function main(): i32 { var p = P { x: 1 }; return p.go(); }`,
		"two inherent": `struct P { x: i32 }
function (p: P) go(): i32 { return 1; }
function (p: P) go(): i32 { return 2; }
function main(): i32 { var p = P { x: 1 }; return p.go(); }`,
		"same assoc fn twice": `struct P { x: i32 }
impl P { function make(): P { return P { x: 1 }; } }
impl P { function make(): P { return P { x: 2 }; } }
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

// A type carrying BOTH an inherent method and a trait-provided one of the
// same name is an ambiguity, not silent shadowing: otherwise `p.go()`
// would mean the trait method inside a generic body (where dispatch goes
// through the bound) and the inherent one at a concrete call site.
func TestInherentAndTraitMethodCollide(t *testing.T) {
	for name, src := range map[string]string{
		"inherent declared second": `struct P { x: i32 }
trait A { function go(self: Self): i32; }
impl A for P { function go(self: Self): i32 { return 1; } }
function (p: P) go(): i32 { return 2; }
function main(): i32 { var p = P { x: 1 }; return p.go(); }`,
		"inherent declared first": `struct P { x: i32 }
function (p: P) go(): i32 { return 2; }
trait A { function go(self: Self): i32; }
impl A for P { function go(self: Self): i32 { return 1; } }
function main(): i32 { var p = P { x: 1 }; return p.go(); }`,
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

// Two traits a module can both name is an ambiguity at the CALL site, and
// the report has to identify each candidate well enough to act on: the
// trait that provided it and where that impl was written.
func TestAmbiguousCallSiteNamesBothTraitsAndPositions(t *testing.T) {
	err := checkSource(t, `struct P { x: i32 }
trait A { function go(self: Self): i32; }
trait B { function go(self: Self): i32; }
impl A for P { function go(self: Self): i32 { return 1; } }
impl B for P { function go(self: Self): i32 { return 2; } }
function main(): i32 { var p = P { x: 1 }; return p.go(); }`)
	if err == nil {
		t.Fatal("expected E074 for a call with two equally-ranked candidates")
	}
	for _, want := range []string{`ambiguous method "go" on P`, "trait A (declared at 4:16)", "trait B (declared at 5:16)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("E074 text is missing %q:\n%v", want, err)
		}
	}
}

// MethodOwners values are sorted regardless of declaration order, so
// nothing downstream inherits source or map-iteration order.
func TestMethodOwnersSorted(t *testing.T) {
	c := newTableChecker()
	for _, trait := range []string{"Zebra", "Mango", "Apple"} {
		mangled := c.mangleMethodName("__method_", "P", "go", trait)
		c.registerMethod("P", "go", trait, mangled, "", ast.Position{})
	}
	wantOwners(t, c.info, "P", "go", []string{"Apple", "Mango", "Zebra"})

	// The first registration keeps the bare mangling every backend and
	// golden already names; the later, differently-owned ones interpose
	// the trait so the flat FuncSigs key stays unique.
	wantTraitMethod(t, c.info, "Zebra", "P", "go", "__method_P_go")
	if got := c.info.TraitMethods["Mango.P.go"]; got != "__method_P__Mango__go" {
		t.Errorf(`TraitMethods["Mango.P.go"] = %q, want "__method_P__Mango__go"`, got)
	}
	if got := c.info.TraitMethods["Apple.P.go"]; got != "__method_P__Apple__go" {
		t.Errorf(`TraitMethods["Apple.P.go"] = %q, want "__method_P__Apple__go"`, got)
	}
}

// resolveMethod is the single lookup every trait-aware reader goes
// through. With no preference it reproduces the flat `Methods` answer
// exactly; with one it selects that trait's own registration.
func TestResolveMethodPrefersNamedTrait(t *testing.T) {
	info := checkInfo(t, `struct P { x: i32 }
trait Area { function area(self: Self): i32; }
impl Area for P { function area(self: Self): i32 { return self.x; } }
function main(): i32 { var p = P { x: 1 }; return p.area(); }`)

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

// An inherent method resolves with an empty owner — that is what tells a
// caller the implementation came from no trait.
func TestResolveMethodInherentHasNoOwner(t *testing.T) {
	info := checkInfo(t, `struct P { x: i32 }
function (p: P) area(): i32 { return p.x; }
function main(): i32 { var p = P { x: 1 }; return p.area(); }`)

	mangled, owner, ok := info.ResolveMethod("P", "area", []string{"Area"})
	if !ok || mangled != "__method_P_area" || owner != "" {
		t.Errorf("ResolveMethod(P, area) = %q, %q, %v; want __method_P_area, \"\", true", mangled, owner, ok)
	}
}

// A preference is matched by SIMPLE name, so a caller can ask for `Ord`
// without knowing the trait arrives module-mangled as `cmp__Ord`.
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

// The property the move/borrow analysis rests on: the owner reported for
// an unpreferred lookup, fed back in as the preference, returns the SAME
// implementation. Without it `methodConsumesReceiver` could read the
// `own` flags of an impl the call site did not dispatch to.
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
	// And each other trait's own registration stays individually
	// addressable — the point of the split index.
	for trait, want := range map[string]string{"Mango": "__method_P__Mango__go", "Apple": "__method_P__Apple__go"} {
		got, gotOwner, ok := c.resolveMethod("P", "go", []string{trait})
		if !ok || got != want || gotOwner != trait {
			t.Errorf("resolveMethod(P, go, [%s]) = %q, %q, %v; want %q, %s, true", trait, got, gotOwner, ok, want, trait)
		}
	}
}
