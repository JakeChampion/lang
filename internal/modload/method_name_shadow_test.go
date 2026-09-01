package modload_test

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
)

// A receiver method keeps its source-level name through the load (the
// hoist to `__method_<Type>_<Name>` needs it), so it never becomes a
// `<mod>__<name>` declaration. It must not claim that name for the
// module's INTERNAL references either: a bare call to a same-named free
// function — a builtin like `env`, or one from an import — was rewritten
// to a `<mod>__env` that nothing declares, so a module could not wrap the
// builtin it named its method after.
//
// Repro: `std/platform`'s `(plat: Platform).env(name)`, whose body is
// `return env(name);`.
func TestMethodNameDoesNotShadowFreeFunctionInsideItsModule(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"wrap.fern": `pub struct Box { v: i32 }
pub function (b: Box) env(name: string): Option[string] {
    return env(name);
}
pub function lookup(b: Box, name: string): Option[string] {
    return b.env(name);
}`,
		"main.fern": `import "wrap";
function main(): i32 {
    var b: wrap.Box = wrap.Box { v: 1 };
    match (wrap.lookup(b, "PATH")) { Some(v) => { return v.len(); }, None => { return 0; } }
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if findFunc(prog, "wrap__env") != nil {
		t.Errorf("a method's name should not produce a `wrap__env` reference; got: %v", funcNames(prog))
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("expected the method body's bare `env(name)` to resolve to the builtin, got %v", err)
	}
}
