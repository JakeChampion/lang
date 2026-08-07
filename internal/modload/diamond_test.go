package modload_test

// Diamond-dependency dedup: a module reached both directly and
// transitively must be combined exactly once, not redeclared.
//
//	    main
//	   /    \
//	  b      c     (main imports b AND c)
//	   \    /
//	     c          (b also imports c)
//
// `c` is reachable two ways from main (directly, and transitively
// through b). docs/PRELUDE-TO-MODULES.md calls this the regression-prone path: the
// loader has to recognise it's already loaded `c` and dedupe, rather
// than combine its decls twice (which would produce a duplicate
// `c__shared` in prog.Funcs and a "redeclared" explosion downstream).
// The existing TestLoadComputesImportClosures covers a *linear* chain
// (a→b→c); this pins the diamond shape where the second path to `c`
// must be a no-op.

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/modload"
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

	// c.shared must be combined exactly once despite being reachable
	// via two paths. A dedup miss shows up as two `c__shared` decls.
	var count int
	for _, fn := range prog.Funcs {
		if fn.Name == "c__shared" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("c.shared should appear exactly once after dedup; got %d copies in %v", count, funcNames(prog))
	}

	// Both qualified call sites — b.via_b's `c.shared()` and main's
	// own `c.shared()` — must rewrite to the same mangled name, so the
	// single combined decl satisfies both.
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

	// Import closure: main reaches both b and c; b reaches c.
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
	// c sits at the bottom of the diamond — it imports nobody, so its
	// closure is just itself.
	if prog.ModuleImports[cAbs][mainAbs] || prog.ModuleImports[cAbs][bAbs] {
		t.Errorf("closure[c] should not reach up to main or b")
	}
}
