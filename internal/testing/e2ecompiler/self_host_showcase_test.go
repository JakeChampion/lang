package e2ecompiler

import (
	"path/filepath"
	"testing"
)

func TestSelfHostShowcaseCore(t *testing.T) {
	path, err := filepath.Abs("../../../examples/showcase/tests/core.fern")
	if err != nil {
		t.Fatal(err)
	}
	testFractalTargets(t, path, true)
}

func TestSelfHostImportedCastTypes(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"main.fern": `import "./store"; function main(): i32 { return store.check(); }`,
		"store.fern": `import "std/option"; import "std/array";
trait Value { function number(self: Self): i32; }
struct Item { n: i32 }
impl Value for Item { function number(self: Self): i32 { return self.n; } }
pub function check(): i32 {
 let items = cell_new([] as Item[]);
 let current = cell_new(None as Option[Item]);
 items.set(items.get().append(Item { n: 42 }));
 current.set(Some(items.get()[0]));
 let Some(value) = current.get() else { return 3; };
 let boxed: dyn Value = value;
 let Some(item) = boxed as? Item else { return 1; };
 if (item.n != 42) { return 2; }
 return 0;
}`,
	})
	testFractalTargets(t, filepath.Join(dir, "main.fern"))
}
