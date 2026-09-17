package x86_64ssa

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// Local labels are per-helper, but the prefix is chosen by hand at each
// emitter, so two helpers can silently pick the same one. Emitting both into
// one module then defines the same label twice, and the later definition
// rebinds every branch that names it — the two bodies run into each other.
//
// That is what #9559 was: __ssa_mismatch (behind __str_eq / __str_ord) and
// __fern_mismatch both used `.Lssa_mm_*`, so any module needing both — which
// is any program that compares strings and calls mismatch, including
// coreutils/uniq.fern and coreutils/sort.fern — failed to assemble on
// `.Lssa_mm_vec`.
//
// The assembler rejects it, so the failure is loud rather than a miscompile.
// This pins it one layer earlier and for every pair at once: no label may be
// DEFINED twice anywhere in an emitted module. Checking the whole module
// rather than the one known pair means the next helper to reuse a prefix
// fails here, not at a user's link.
var labelDefRe = regexp.MustCompile(`(?m)^(\.L[A-Za-z0-9_.$]+):`)

func TestNoLocalLabelIsDefinedTwiceInOneModule(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()

	a := constStr(f, e, "banana")
	b := constStr(f, e, "bandana")
	// __str_eq and __str_ord pull in __ssa_mismatch; __fern_mismatch is the
	// separate five-argument kernel. A module wanting all three is what
	// #9559 could not assemble.
	eq := callOp(f, e, "__str_eq", a, b)
	ord := callOp(f, e, "__str_ord", a, b)
	mm := callOp(f, e, "__fern_mismatch", a, constOp(f, e, 0), b, constOp(f, e, 0), constOp(f, e, 6))

	sum := f.AddOp(e, ssa.OpAdd, eq, f.AddOp(e, ssa.OpAdd, ord, mm))
	f.SetRet(e, sum)

	asm, err := EmitAsmModule(map[string]*ssa.Func{"main": f}, "main", 8, nil)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	// The module has to actually contain both kernels, or this test passes
	// by checking nothing.
	for _, sym := range []string{mismatchSym + ":", "fn___fern_mismatch:"} {
		if !strings.Contains(asm, sym) {
			t.Fatalf("no %s in the emitted module, so the two-kernel case was never built", sym)
		}
	}

	seen := map[string]bool{}
	var dupes []string
	for _, m := range labelDefRe.FindAllStringSubmatch(asm, -1) {
		if seen[m[1]] {
			dupes = append(dupes, m[1])
			continue
		}
		seen[m[1]] = true
	}
	if len(dupes) > 0 {
		t.Errorf("these local labels are defined more than once in one module: %s\n\n"+
			"Two helpers sharing a label prefix means the later definition rebinds every branch "+
			"that names it, so the bodies run into each other; the assembler rejects the module "+
			"outright (#9559). Give each helper a prefix of its own — the stat family threads `lp` "+
			"through emitStatLikeHelper for exactly this reason.", strings.Join(dupes, ", "))
	}
}
