package modload_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

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
    let m: Map[i32, V] = map_new(8);
    m = m.insert(1, V { n: 10 });
    return m.len();
}`,
		},
		{
			name: "key struct named K, scalar-keyed map",
			src: `import "core/map";
struct K { a: i32 }
function main(): i32 {
    let m: Map[i32, string] = map_new(8);
    m = m.insert(1, "x");
    return m.len();
}`,
		},
		{
			name: "struct named T against std/array",
			src: `import "std/array";
struct T { a: i32 }
function main(): i32 {
    let xs: i32[] = [3, 1, 2];
    return xs.fold(0, (acc: i32, x: i32) => acc + x);
}`,
		},
		{
			name: "both K and V declared",
			src: `import "core/map";
struct K { a: i32 }
struct V { n: i32 }
function main(): i32 {
    let m: Map[i32, V] = map_new(8);
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

func TestReceiverTypeVarsKeepVisibleTypesConcrete(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern": `struct Payload { n: i32 }
struct Other { n: i32 }
struct Box[T] { v: T }
function (b: Box[Payload]) get(): i32 { return b.v.n; }
function main(): i32 {
    let o: Box[Other] = Box { v: Other { n: 1 } };
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

func TestReceiverTypeVarsSeeImportedTypes(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"payload.fern": `pub struct Payload { n: i32 }`,
		"boxes.fern": `import "./payload";
pub struct Box[T] { v: T }
pub function (b: Box[payload.Payload]) get(): i32 { return b.v.n; }`,
		"main.fern": `import "./boxes";
import "./payload";
function main(): i32 {
    let b: boxes.Box[payload.Payload] = boxes.Box { v: payload.Payload { n: 7 } };
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

func TestReceiverTypeVarCollisionAcrossUserModules(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"boxes.fern": `pub struct Box[T] { v: T }
pub function (b: Box[T]) unwrap(): T { return b.v; }`,
		"main.fern": `import "./boxes";
struct T { n: i32 }
function main(): i32 {
    let b: boxes.Box[T] = boxes.Box { v: T { n: 5 } };
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

func TestCollidingNameDiagnosticsNameTheUsersFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.fern": `import "core/map";
struct K { a: i32 }
function main(): i32 {
    let m: Map[K, string] = map_new(8);
    return m.len();
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
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
