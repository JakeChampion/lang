package arm64ssa_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// emitIdxAsm renders a module, failing the test rather than returning an error.
func emitIdxAsm(t *testing.T, funcs map[string]*ssa.Func, entry string) string {
	t.Helper()
	asm, err := arm64ssa.EmitAsmModule(funcs, entry, arm64ssa.DefaultNumAlloc, nil)
	if err != nil {
		t.Fatalf("EmitAsmModule: %v", err)
	}
	return asm
}

// idxFuncBody returns just the named function's lines, from its own label to
// the next top-level one.
//
// Scoping matters for any assertion about the shape of an inlined index. The
// helper body is STILL emitted — referencedRuntimeHelpers decides that from the
// pre-inline instruction list, so a CallPair site keeps a real callee — and each
// helper's own text contains the length read, the compare, the branch and the
// 134. Searching the whole module therefore cannot tell an inline that kept the
// check from one that dropped it.
func idxFuncBody(t *testing.T, asm, name string) string {
	t.Helper()
	lines := strings.Split(asm, "\n")
	start := -1
	for i, l := range lines {
		if l == "fn_"+name+":" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("no fn_%s: label in the emitted module", name)
	}
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "fn_") && strings.HasSuffix(lines[i], ":") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	return strings.Join(lines[start:], "\n")
}

// An array index is address arithmetic — four instructions or fewer — so a call
// costs more than the work: the allocator has to spill every caller-saved
// register holding a value live across it, and indexing sits in the innermost
// loop of anything that walks an array. cmp.sort's monomorphised body made 22
// such calls where the stack-machine backend makes none.
//
// The bounds check has to survive inlining, and it is essential beyond the
// trap: `cmp w` rejects a negative index as a huge unsigned, which is what makes
// the full-width add below it safe.
func TestArrayIndexIsInlinedNotCalled(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	arr := addrCallOp(f, b, "__alloc_u8", constOp(f, b, 16))
	elem := addrCallOp(f, b, "__arr_idx_1", arr, constOp(f, b, 0))
	f.SetRet(b, load8u(f, b, elem, 0))

	asm := emitIdxAsm(t, map[string]*ssa.Func{"main": f}, "main")
	if strings.Contains(asm, "bl "+"fn___arr_idx") {
		t.Error("array index still emitted as a call")
	}
	// Scoped to main: the helper bodies the module still carries contain all
	// four of these themselves, so a module-wide search would pass on an inline
	// that dropped the check entirely.
	body := idxFuncBody(t, asm, "main")
	for _, want := range []string{"cmp", "b.lo", "#134"} {
		if !strings.Contains(body, want) {
			t.Errorf("inlined index is missing %q — the bounds check must survive\n%s", want, body)
		}
	}
	// The length lives in a header BELOW the buffer, reached by the unscaled
	// form — which is what separates the array shape from the slice shape.
	if !regexp.MustCompile(`ldur\s+w\d+, \[x\d+, #-4\]`).MatchString(body) {
		t.Errorf("inlined array index never reads the length header at -4\n%s", body)
	}
}

// Every site needs its own ok-label: two indexes in one function otherwise emit
// the same label twice and the assembler rejects the module.
func TestTwoIndexesInOneFunctionGetDistinctLabels(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	arr := addrCallOp(f, b, "__alloc_u8", constOp(f, b, 16))
	a := load8u(f, b, addrCallOp(f, b, "__arr_idx_1", arr, constOp(f, b, 0)), 0)
	c := load8u(f, b, addrCallOp(f, b, "__arr_idx_1", arr, constOp(f, b, 1)), 0)
	f.SetRet(b, f.AddOp(b, ssa.OpAdd, a, c))

	asm := emitIdxAsm(t, map[string]*ssa.Func{"main": f}, "main")
	seen := map[string]bool{}
	for _, l := range strings.Split(asm, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, ".Lssa_idx_") || !strings.HasSuffix(l, ":") {
			continue
		}
		if seen[l] {
			t.Errorf("duplicate index label %q — the assembler rejects a repeated label", l)
		}
		seen[l] = true
	}
	if len(seen) < 2 {
		t.Errorf("expected two inlined index sites, saw %d: %v", len(seen), seen)
	}
}

// __slice_idx_1 is the spelling that matters most, and it was the one this
// table skipped: a byte scan written the way every utility in coreutils/ writes
// it (chunk.as_bytes(), then an indexed walk) reaches the slice helper, never
// the array one. Both stack-machine backends inline it; this one called it.
//
// A view is one indirection further out than a buffer, so the inline form reads
// two fields the array form does not: the length at [base+8] and the data
// pointer at [base+0]. TestArmRunSliceIdxWalksTheView and its bounds-check and
// stride siblings in slice_test.go are what pin the behaviour; this pins that
// the behaviour is reached without a call.
func TestSliceIndexIsInlinedNotCalled(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	hdr := sliceHeaderOf(f, b, "abc")
	f.SetRet(b, load8u(f, b, addrCallOp(f, b, "__slice_idx_1", hdr, constOp(f, b, 1)), 0))

	asm := emitIdxAsm(t, map[string]*ssa.Func{"main": f}, "main")
	if strings.Contains(asm, "bl "+"fn___slice_idx") {
		t.Error("slice index still emitted as a call")
	}
	// A 32-bit load at +8, inside main. The w register is the discriminator:
	// it separates the length read from the inline's own 64-bit data-pointer
	// load at +0, and the scoping keeps the helper's identical read out of the
	// answer (see idxFuncBody).
	body := idxFuncBody(t, asm, "main")
	if !regexp.MustCompile(`ldr\s+w\d+, \[x\d+, #8\]`).MatchString(body) {
		t.Errorf("inlined slice index never loads the length field at +8\n%s", body)
	}
	for _, want := range []string{"cmp", "b.lo", "#134"} {
		if !strings.Contains(body, want) {
			t.Errorf("inlined slice index is missing %q — the bounds check must survive\n%s", want, body)
		}
	}
}
