package fernrt

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// The targets the backends ask with.
var targets = []Target{
	{PtrW: 8, OS: "linux", Arch: "x86-64"},
	{PtrW: 8, OS: "linux", Arch: "arm64"},
	{PtrW: 8, OS: "darwin", Arch: "arm64"},
	{PtrW: 4, OS: "wasi", Arch: "wasm32"},
}

// The source must front-end cleanly and lower for every target a backend
// asks with; a helper that fails here would fail inside every Emit that
// needs it.
func TestEveryHelperLowersForEveryTarget(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("runtime.fern defines no helpers")
	}
	for _, tg := range targets {
		for _, name := range names {
			decl, fn, err := Func(name, tg)
			if err != nil {
				t.Fatalf("Func(%q, %v): %v", name, tg, err)
			}
			if decl.Name != name || fn.Name != name {
				t.Errorf("Func(%q, %v) returned %q / %q", name, tg, decl.Name, fn.Name)
			}
			if fn.PtrW != tg.PtrW {
				t.Errorf("%s for %v lowered with PtrW=%d", name, tg, fn.PtrW)
			}
			if len(fn.Ops) == 0 {
				t.Errorf("%s for %v lowered to no ops", name, tg)
			}
		}
	}
	if _, _, err := Func("__fern_no_such_helper", targets[0]); err == nil {
		t.Error("Func on an undefined helper returned no error")
	}
	if Has("__fern_no_such_helper") || !Has("__fern_utf8_valid") {
		t.Error("Has disagrees with the source")
	}
}

// A helper may only reach the provided-callee floor. A call to a function
// defined in runtime.fern is fine too (it is emitted alongside); a call to
// anything else is a link failure on every backend, and a call to the
// operation the helper implements would be the circularity the floor exists
// to avoid.
func TestHelpersCallOnlyTheFloorOrEachOther(t *testing.T) {
	for _, tg := range targets {
		for _, name := range Names() {
			_, fn, err := Func(name, tg)
			if err != nil {
				t.Fatal(err)
			}
			checkFloorOnly(t, name, fn)
		}
	}
}

func checkFloorOnly(t *testing.T, name string, fn *ir.Func) {
	t.Helper()
	{
		for _, op := range fn.Ops {
			switch op.Kind {
			case ir.OpCallDirect:
				if Has(op.Str) {
					continue
				}
				if _, _, ok := ir.ProvidedCallee(op.Str, false); !ok {
					t.Errorf("%s calls %q, which is neither a runtime.fern helper nor a provided callee", name, op.Str)
				}
			case ir.OpCallIndirect, ir.OpCallClosureDirect, ir.OpMakeClosure, ir.OpMakeEnv, ir.OpStrConcat, ir.OpStrEq, ir.OpStrCmp, ir.OpAlloc:
				t.Errorf("%s uses %v, which needs a runtime helper of its own", name, op.Kind)
			}
		}
	}
}

// The helpers lower under -cover too. They are not the program under
// measurement, and ir.LowerWith refuses an uninstrumented lowering while
// ast.CoverEnabled is set unless told the lowering is exempt — every
// `fern -cover` build of a program that needs a helper failed that way.
func TestHelpersLowerUnderCover(t *testing.T) {
	mu.Lock()
	cache = map[cacheKey]*lowered{}
	mu.Unlock()
	prev := ast.CoverEnabled
	ast.CoverEnabled = true
	t.Cleanup(func() {
		ast.CoverEnabled = prev
		mu.Lock()
		cache = map[cacheKey]*lowered{}
		mu.Unlock()
	})
	_, fn, err := Func("__fern_utf8_valid", targets[0])
	if err != nil {
		t.Fatalf("Func under -cover: %v", err)
	}
	for _, op := range fn.Ops {
		if op.Kind == ir.OpCoverPoint {
			t.Fatal("a helper lowered under -cover carries cover points; the program's counters are not its")
		}
	}
}
