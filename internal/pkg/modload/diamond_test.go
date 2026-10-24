package modload_test

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestLoadDedupesDiamondImport(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"c.fern": `pub function shared(): i32 { return 7; }`,
		"b.fern": `import "./c";
pub function via_b(): i32 { return c.shared() + 1; }`,
		"main.fern": `import "./b";
import "./c";
function main(): i32 { return b.via_b() + c.shared(); }`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	var count int
	for _, fn := range prog.Funcs {
		if fn.Name == "c__shared" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("c.shared should appear exactly once after dedup; got %d copies in %v", count, funcNames(prog))
	}

	if b := findFunc(prog, "b__via_b"); b == nil {
		t.Errorf("expected b's function mangled as b__via_b; got %v", funcNames(prog))
	} else if !callsDirect(b, "c__shared") {
		t.Errorf("b__via_b should call c__shared directly")
	}
	main := findFunc(prog, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	if !callsDirect(main, "b__via_b") {
		t.Errorf("main should call b__via_b directly")
	}
	if !callsDirect(main, "c__shared") {
		t.Errorf("main's direct `c.shared()` should also resolve to c__shared")
	}

	mainAbs, _ := filepath.Abs(filepath.Join(dir, "main.fern"))
	bAbs, _ := filepath.Abs(filepath.Join(dir, "b.fern"))
	cAbs, _ := filepath.Abs(filepath.Join(dir, "c.fern"))
	if !prog.ModuleImports[mainAbs][bAbs] {
		t.Errorf("closure[main] should contain b (direct)")
	}
	if !prog.ModuleImports[mainAbs][cAbs] {
		t.Errorf("closure[main] should contain c (direct + transitive)")
	}
	if !prog.ModuleImports[bAbs][cAbs] {
		t.Errorf("closure[b] should contain c (direct)")
	}
	if prog.ModuleImports[cAbs][mainAbs] || prog.ModuleImports[cAbs][bAbs] {
		t.Errorf("closure[c] should not reach up to main or b")
	}
}
