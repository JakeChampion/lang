package x86_64ssa

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// addrCallOp is callOp for a callee whose result fills the whole register — a
// heap pointer. ssa.ResolveWidths reads that off the callee's ssa.Func, which a
// runtime helper does not have, so a module built by hand has to say it here or
// the address comes back i32-masked.
func addrCallOp(f *ssa.Func, b *ssa.Block, callee string, args ...ssa.Value) ssa.Value {
	v := f.AddOp(b, ssa.OpCall, args...)
	op := b.Ops[len(b.Ops)-1]
	op.Str, op.Width, op.Addr = callee, 64, true
	return v
}

// sliceView builds the 16-byte view header __slice_idx_* dereferences: the full
// data pointer at +0 and the length at +8. The length is written as a full word
// because the helper reads only its low 32 bits.
func sliceView(f *ssa.Func, b *ssa.Block, data ssa.Value, length int64) ssa.Value {
	view := f.AddOp(b, ssa.OpAlloc, constOp(f, b, 16))
	storeMem(f, b, view, 0, data, ssa.OpStore)
	storeMem(f, b, view, 8, constOp(f, b, length), ssa.OpStore)
	return view
}

// byteBuf allocates a length-prefixed byte array holding vals.
func byteBuf(f *ssa.Func, b *ssa.Block, vals ...int64) ssa.Value {
	buf := addrCallOp(f, b, "__alloc_u8", constOp(f, b, int64(len(vals))))
	for i, v := range vals {
		storeMem(f, b, buf, int64(i), constOp(f, b, v), ssa.OpStore8)
	}
	return buf
}

func idxAsm(t *testing.T, funcs map[string]*ssa.Func, entry string) string {
	t.Helper()
	asm, err := EmitAsmModule(funcs, entry, DefaultNumAlloc, nil)
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
// pre-inline instruction list, so a CallPair site keeps a real callee — and
// emitSliceIdxHelper's own text contains the length read, the compare, the
// branch and the 134, as does the store a test's own sliceView makes at offset
// 8. Searching the whole module therefore cannot tell an inline that reads the
// right field from one that reads the wrong field.
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

// An index is address arithmetic — a compare and an lea — so a call costs more
// than the work: the allocator has to spill every caller-saved register holding
// a value live across it, and indexing sits in the innermost loop of anything
// that walks an array. Both stack-machine backends have inlined these all along
// (emitInlineIdxHelper); this backend had no inline form at all.
//
// The bounds check has to survive inlining. It is essential beyond the trap:
// the single unsigned compare rejects a negative index as a huge unsigned, which
// is what makes the address calculation below it safe.
func TestArrayIndexIsInlinedNotCalled(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	arr := addrCallOp(f, b, "__alloc_u8", constOp(f, b, 16))
	f.SetRet(b, loadMem(f, b, addrCallOp(f, b, "__arr_idx_1", arr, constOp(f, b, 0)), 0, ssa.OpLoad8U))

	asm := idxAsm(t, map[string]*ssa.Func{"main": f}, "main")
	if strings.Contains(asm, "call fn___arr_idx") {
		t.Error("array index still emitted as a call")
	}
	// Scoped to main: the helper bodies the module still carries contain all
	// three of these tokens themselves, so a module-wide search would pass on
	// an inline that dropped the check entirely.
	body := idxFuncBody(t, asm, "main")
	for _, want := range []string{"cmp", "jb", "134"} {
		if !strings.Contains(body, want) {
			t.Errorf("inlined index is missing %q — the bounds check must survive\n%s", want, body)
		}
	}
	// The length lives in a header BELOW the buffer, which is what separates the
	// array form from the slice form.
	if !regexp.MustCompile(`\[[a-z0-9]+ - 4\]`).MatchString(body) {
		t.Errorf("inlined array index never reads the length header at -4\n%s", body)
	}
}

// __slice_idx_1 is the spelling that matters most: a byte scan written the way
// every utility in coreutils/ writes it (chunk.as_bytes(), then an indexed walk)
// reaches the slice helper, never the array one.
//
// A view is one indirection further out, so the inline form has to read two
// fields the array form does not: the length at +8 and the data pointer at +0.
func TestSliceIndexIsInlinedNotCalled(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	view := sliceView(f, b, byteBuf(f, b, 3, 4), 2)
	f.SetRet(b, loadMem(f, b, addrCallOp(f, b, "__slice_idx_1", view, constOp(f, b, 1)), 0, ssa.OpLoad8U))

	asm := idxAsm(t, map[string]*ssa.Func{"main": f}, "main")
	if strings.Contains(asm, "call fn___slice_idx") {
		t.Error("slice index still emitted as a call")
	}
	// A 32-bit LOAD from +8, inside main. Both halves of that are needed: the
	// module at large contains the helper's identical read (see idxFuncBody),
	// and main itself contains sliceView's `mov [reg + 8], reg` STORE building
	// the header. Requiring a 32-bit destination separates the inline's length
	// read from the store and from its own 64-bit data-pointer load at +0.
	body := idxFuncBody(t, asm, "main")
	lenLoad := regexp.MustCompile(`(?m)^\s*mov (e[a-z]{2}|r\d+d), \[[a-z0-9]+ \+ 8\]\s*$`)
	if !lenLoad.MatchString(body) {
		t.Errorf("inlined slice index never loads the length field at +8\n%s", body)
	}
	if !strings.Contains(body, "134") {
		t.Error("inlined slice index dropped the out-of-range trap")
	}
}

// Every site needs its own ok-label: two indexes in one function otherwise emit
// the same label twice and the assembler rejects the module.
func TestTwoIndexesInOneFunctionGetDistinctLabels(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	arr := addrCallOp(f, b, "__alloc_u8", constOp(f, b, 16))
	a := loadMem(f, b, addrCallOp(f, b, "__arr_idx_1", arr, constOp(f, b, 0)), 0, ssa.OpLoad8U)
	c := loadMem(f, b, addrCallOp(f, b, "__arr_idx_1", arr, constOp(f, b, 1)), 0, ssa.OpLoad8U)
	f.SetRet(b, f.AddOp(b, ssa.OpAdd, a, c))

	asm := idxAsm(t, map[string]*ssa.Func{"main": f}, "main")
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

// The inline form has to answer what the call answered. Swept over small
// register files so the operands arrive from slots as well as from registers —
// a slot-homed base or index takes a different path through the emitter.
func TestInlinedIndexReadsTheSameBytes(t *testing.T) {
	cases := []struct {
		name string
		want int
		body func(f *ssa.Func, b *ssa.Block) ssa.Value
	}{
		{"arr_idx_1", 7 + 11, func(f *ssa.Func, b *ssa.Block) ssa.Value {
			buf := byteBuf(f, b, 7, 11)
			a := loadMem(f, b, addrCallOp(f, b, "__arr_idx_1", buf, constOp(f, b, 0)), 0, ssa.OpLoad8U)
			c := loadMem(f, b, addrCallOp(f, b, "__arr_idx_1", buf, constOp(f, b, 1)), 0, ssa.OpLoad8U)
			return f.AddOp(b, ssa.OpAdd, a, c)
		}},
		{"slice_idx_1", 9 + 13, func(f *ssa.Func, b *ssa.Block) ssa.Value {
			view := sliceView(f, b, byteBuf(f, b, 9, 13), 2)
			a := loadMem(f, b, addrCallOp(f, b, "__slice_idx_1", view, constOp(f, b, 0)), 0, ssa.OpLoad8U)
			c := loadMem(f, b, addrCallOp(f, b, "__slice_idx_1", view, constOp(f, b, 1)), 0, ssa.OpLoad8U)
			return f.AddOp(b, ssa.OpAdd, a, c)
		}},
		// A stride the lea cannot scale, so the shift is spelled out — through a
		// scratch register, because unlike the helper's dead argument register
		// the index here may still be live.
		{"arr_idx_16", 21, func(f *ssa.Func, b *ssa.Block) ssa.Value {
			buf := addrCallOp(f, b, "__alloc_u8", constOp(f, b, 32))
			storeMem(f, b, buf, 16, constOp(f, b, 21), ssa.OpStore8)
			return loadMem(f, b, addrCallOp(f, b, "__arr_idx_16", buf, constOp(f, b, 1)), 0, ssa.OpLoad8U)
		}},
	}
	for _, tc := range cases {
		for _, nAlloc := range []int{2, 3, 8, DefaultNumAlloc} {
			f := ssa.NewFunc("main")
			b := f.NewBlock()
			f.SetRet(b, tc.body(f, b))
			if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", nAlloc, nil); got != tc.want {
				t.Errorf("%s nAlloc=%d = %d, want %d", tc.name, nAlloc, got, tc.want)
			}
		}
	}
}

// Out of range still exits 134. The inline form keeps the helper's trap, so a
// program that indexes past the end dies the same way it did through the call —
// and an index whose low half is in range but whose top half is not is caught by
// the slice form's re-narrowing move.
func TestInlinedIndexTrapsOutOfRange(t *testing.T) {
	for _, tc := range []struct {
		name   string
		callee string
		idx    int64
	}{
		{"array past the end", "__arr_idx_1", 4},
		{"slice past the end", "__slice_idx_1", 4},
		{"array negative", "__arr_idx_1", -1},
		{"slice negative", "__slice_idx_1", -1},
	} {
		f := ssa.NewFunc("main")
		b := f.NewBlock()
		buf := byteBuf(f, b, 1, 2)
		base := buf
		if strings.HasPrefix(tc.callee, "__slice") {
			base = sliceView(f, b, buf, 2)
		}
		f.SetRet(b, loadMem(f, b, addrCallOp(f, b, tc.callee, base, constOp(f, b, tc.idx)), 0, ssa.OpLoad8U))
		if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", DefaultNumAlloc, nil); got != 134 {
			t.Errorf("%s: exit %d, want 134", tc.name, got)
		}
	}
}
