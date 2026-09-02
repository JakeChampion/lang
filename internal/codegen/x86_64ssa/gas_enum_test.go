package x86_64ssa

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// A payloadless enum sentinel: the value is a pointer to a shared static cell
// whose tag sits at offset 0, so reading it back yields the tag. Diffed against
// ssa.Eval, which models sentinels the same way.
func TestAsmRunEnumSentinel(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("s")
		e := f.NewBlock()
		s := enumSentinel(f, e, 2)
		f.SetRet(e, loadMem(f, e, s, 0, ssa.OpLoad8U))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, build(), n)
	}
}

// The property the shared cell exists for: same tag is the SAME address, so two
// `None`s compare equal, and two different tags do not. A per-site cell would
// pass the tag read-back above and fail here.
func TestAsmRunEnumSentinelIdentity(t *testing.T) {
	build := func() *ssa.Func {
		f := ssa.NewFunc("s")
		e := f.NewBlock()
		a := enumSentinel(f, e, 1)
		b := enumSentinel(f, e, 1)
		c := enumSentinel(f, e, 3)
		same := f.AddOp(e, ssa.OpEq, a, b) // 1 — one cell for tag 1
		diff := f.AddOp(e, ssa.OpEq, a, c) // 0 — tag 3 is a different cell
		f.SetRet(e, f.AddOp(e, ssa.OpAdd,
			f.AddOp(e, ssa.OpMul, same, constOp(f, e, 10)),
			f.AddOp(e, ssa.OpAdd, diff, loadMem(f, e, c, 0, ssa.OpLoad8U))))
		return f
	}
	for _, n := range []int{1, 2, 8} {
		runMatchesEval(t, build(), n) // 13
	}
}

// Sharing is MODULE-wide, not function-wide: a sentinel produced in one function
// and one produced in another must be the same pointer, or an Option returned
// across a call stops matching at the caller.
func TestAsmRunModuleEnumSentinelSharedAcrossFuncs(t *testing.T) {
	none := ssa.NewFunc("none")
	ne := none.NewBlock()
	none.SetRet(ne, enumSentinel(none, ne, 4))

	main := ssa.NewFunc("main")
	me := main.NewBlock()
	mine := enumSentinel(main, me, 4)
	theirs := callOp(main, me, "none")
	main.SetRet(me, main.AddOp(me, ssa.OpEq, mine, theirs))

	funcs := map[string]*ssa.Func{"none": none, "main": main}
	for _, n := range []int{2, 8} {
		runModuleMatchesEval(t, funcs, "main", n, nil) // 1
	}
}

// The cell carries the string literals' immortal rc header, so a drop of a
// sentinel-valued Option short-circuits instead of writing to .rodata. Pinned on
// the text because a wrong header is a fault at run time in a program that
// happens to drop, not in one that only reads the tag.
func TestAsmEnumSentinelCellHasImmortalHeader(t *testing.T) {
	f := ssa.NewFunc("s")
	e := f.NewBlock()
	f.SetRet(e, loadMem(f, e, enumSentinel(f, e, 6), 0, ssa.OpLoad8U))
	asm, err := EmitAsmModule(map[string]*ssa.Func{"s": f}, "s", 8, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	want := "\t.4byte 0x80000000\n\t.4byte 0\nsent_0:\n\t.4byte 6\n"
	if !strings.Contains(asm, want) {
		t.Errorf("sentinel cell not emitted with the immortal header; want\n%s\ngot:\n%s", want, asm)
	}
}
