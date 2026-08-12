package modload_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/modload"
)

// A method's implicit receiver type variables (`K` / `V` in
// `(m: Map[K, V]) merge(…)`) are the receiver names that do not resolve
// to a struct or enum. The merged program is flat, so before #6118 that
// test consulted EVERY module's types at once: declaring `struct V` in
// the importing module made core/map's own `V` look concrete, the
// methods bound one type parameter instead of two, and the program
// failed to check with errors reported inside stdlib source.
//
// The declaring module cannot name a type it does not import, so a
// collision from outside its import closure must not be visible to it.
func TestReceiverTypeVarsIgnoreTypesTheModuleCannotSee(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "value struct named V",
			src: `import "core/map";
struct V { n: i32 }
function main(): i32 {
    var m: Map[i32, V] = map_new(8);
    m = m.insert(1, V { n: 10 });
    return m.len();
}`,
		},
		{
			// The trigger is the NAME, not the position: a struct named K
			// collides with the key parameter even for a scalar-keyed map.
			name: "key struct named K, scalar-keyed map",
			src: `import "core/map";
struct K { a: i32 }
function main(): i32 {
    var m: Map[i32, string] = map_new(8);
    m = m.insert(1, "x");
    return m.len();
}`,
		},
		{
			// Not specific to core/map: std/array's element-polymorphic
			// methods are `(xs: T[])`, so a struct named T disabled the
			// whole array method surface at once.
			name: "struct named T against std/array",
			src: `import "std/array";
struct T { a: i32 }
function main(): i32 {
    var xs: i32[] = [3, 1, 2];
    return xs.fold(0, (acc: i32, x: i32) => acc + x);
}`,
		},
		{
			name: "both K and V declared",
			src: `import "core/map";
struct K { a: i32 }
struct V { n: i32 }
function main(): i32 {
    var m: Map[i32, V] = map_new(8);
    m = m.insert(1, V { n: 10 });
    return m.len();
}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{"main.fern": tc.src})
			prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := checker.Check(prog); err != nil {
				t.Fatalf("expected a clean check, got %v", err)
			}
		})
	}
}

// The scoping must not turn every receiver name into a type variable: a
// struct the declaring module CAN see is still a concrete instantiation,
// so `(b: Box[Payload]) …` binds nothing and only accepts a Box[Payload].
func TestReceiverTypeVarsKeepVisibleTypesConcrete(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern": `struct Payload { n: i32 }
struct Other { n: i32 }
struct Box[T] { v: T }
function (b: Box[Payload]) get(): i32 { return b.v.n; }
function main(): i32 {
    var o: Box[Other] = Box { v: Other { n: 1 } };
    return o.get();
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err == nil {
		t.Fatal("expected Box[Other].get() to be rejected — Box[Payload] is a concrete receiver, not a generic one")
	}
}

// The same concreteness holds across an import: a struct the declaring
// module reaches through its own `import` is visible to it, so it pins
// the receiver rather than becoming a free type variable.
func TestReceiverTypeVarsSeeImportedTypes(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"payload.fern": `pub struct Payload { n: i32 }`,
		"boxes.fern": `import "./payload";
pub struct Box[T] { v: T }
pub function (b: Box[payload.Payload]) get(): i32 { return b.v.n; }`,
		"main.fern": `import "./boxes";
import "./payload";
function main(): i32 {
    var b: boxes.Box[payload.Payload] = boxes.Box { v: payload.Payload { n: 7 } };
    return b.get();
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("expected a clean check, got %v", err)
	}
}

// A local struct whose name matches an imported module's receiver type
// variable must not leak into that module — the importing side keeps
// using its own type, and neither side reports an error.
func TestReceiverTypeVarCollisionAcrossUserModules(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"boxes.fern": `pub struct Box[T] { v: T }
pub function (b: Box[T]) unwrap(): T { return b.v; }`,
		"main.fern": `import "./boxes";
struct T { n: i32 }
function main(): i32 {
    var b: boxes.Box[T] = boxes.Box { v: T { n: 5 } };
    return b.unwrap().n;
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("expected a clean check, got %v", err)
	}
}

// Whatever diagnostics a colliding-name program does produce must point
// at the user's own file. The pre-fix failure mode reported errors only
// inside stdlib://core/map.fern, for code the user never wrote.
func TestCollidingNameDiagnosticsNameTheUsersFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern": `import "core/map";
struct K { a: i32 }
function main(): i32 {
    var m: Map[K, string] = map_new(8);
    return m.len();
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	// A struct key still needs @derive(Eq, Hash) — that error is correct
	// and identical for a struct named `Key`. What matters is where it
	// points.
	_, cerr := checker.Check(prog)
	if cerr == nil {
		t.Fatal("expected the missing @derive(Eq, Hash) error")
	}
	errs, ok := cerr.(diag.Errors)
	if !ok {
		t.Fatalf("checker error is %T, want diag.Errors", cerr)
	}
	for _, e := range errs {
		ce, ok := e.(*checker.Error)
		if !ok {
			t.Fatalf("entry is %T, want *checker.Error", e)
		}
		if strings.HasPrefix(ce.Path, "stdlib://") {
			t.Errorf("diagnostic points into stdlib source (%s): %v", ce.Path, ce)
		}
		if !strings.HasSuffix(ce.Path, "main.fern") {
			t.Errorf("diagnostic path %q does not name the user's file: %v", ce.Path, ce)
		}
	}
}
