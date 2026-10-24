package modload_test

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

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
    let b: wrap.Box = wrap.Box { v: 1 };
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
