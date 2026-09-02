package arm64ssa_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// A runtime helper written in Fern (internal/fernrt) is lifted into the
// module and emitted under the label its hand-written callers use — once,
// with no hand-written twin. read_file's body is what reaches it here. The raw
// poke that body is written on inlines at its call site (pokeInline) exactly as
// it does in user code, so no helper of that name is emitted or called.
func TestFernRuntimeHelperLiftedIntoModule(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	f.SetRet(b, addrCallOp(f, b, "read_file", constOp(f, b, 0)))
	asm := emitIdxAsm(t, map[string]*ssa.Func{"main": f}, "main")
	if n := strings.Count(asm, "\nfn___fern_utf8_valid:"); n != 1 {
		t.Errorf("fn___fern_utf8_valid defined %d times, want 1", n)
	}
	if !strings.Contains(asm, "bl fn___fern_utf8_valid") {
		t.Error("read_file does not call fn___fern_utf8_valid")
	}
	if n := strings.Count(asm, "\nfn___load_u8:"); n != 0 {
		t.Errorf("fn___load_u8 defined %d times, want 0 — the poke inlines", n)
	}
	if strings.Contains(asm, "bl fn___load_u8") {
		t.Error("__load_u8 is reached by a call rather than inlined")
	}

	g := ssa.NewFunc("main")
	gb := g.NewBlock()
	g.SetRet(gb, constOp(g, gb, 0))
	if asm := emitIdxAsm(t, map[string]*ssa.Func{"main": g}, "main"); strings.Contains(asm, "fern_utf8_valid") {
		t.Error("the helper is emitted for a module that never reads a file")
	}
}
