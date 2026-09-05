package x86_64ssa

import (
	"bytes"
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// The four helpers this file covers are what took the x86-64 SSA corpus
// differential from 29 comparable programs to 39 (#8570): `print` and `eprint`
// are in almost every program, `__alloc_reuse` in every one the reuse pass
// touches, and `__fern_drop_arr_str` in every one holding a string array.
//
// Each is checked against the behaviour of the stack-machine backend's twin,
// because the corpus differential's whole premise is that the two agree.

// runModuleOutput runs the module and returns its stdout, stderr and exit code
// — the three the corpus differential compares.
func runModuleOutput(t *testing.T, funcs map[string]*ssa.Func, entry string, numAlloc int) (string, string, int) {
	t.Helper()
	bin := assembleModuleBinary(t, funcs, entry, numAlloc, nil)
	cmd := exec.Command(bin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	return out.String(), errb.String(), cmd.ProcessState.ExitCode()
}

// print writes the string and a newline to stdout; eprint writes to stderr.
// Both in one program, so the test also pins that they do not share a stream:
// a helper that wrote everything to fd 1 would pass a stdout-only assertion.
func TestAsmRunPrintAndEprintStreams(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	callOp(f, e, "print", constStr(f, e, "out one"))
	callOp(f, e, "eprint", constStr(f, e, "err one"))
	callOp(f, e, "print", constStr(f, e, "out two"))
	f.SetRet(e, constOp(f, e, 0))

	stdout, stderr, code := runModuleOutput(t, map[string]*ssa.Func{"main": f}, "main", 8)
	if stdout != "out one\nout two\n" {
		t.Errorf("stdout = %q, want %q", stdout, "out one\nout two\n")
	}
	if stderr != "err one\n" {
		t.Errorf("stderr = %q, want %q", stderr, "err one\n")
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
}

// The empty string still writes its newline: `print("")` is a blank line, not
// nothing, and a helper that skipped a zero-length write would drop it.
func TestAsmRunPrintEmptyString(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	callOp(f, e, "print", constStr(f, e, ""))
	f.SetRet(e, constOp(f, e, 0))

	stdout, _, _ := runModuleOutput(t, map[string]*ssa.Func{"main": f}, "main", 8)
	if stdout != "\n" {
		t.Errorf("stdout = %q, want a bare newline", stdout)
	}
}

// print returns its argument unchanged — every rc-neutral helper here does, and
// the IR is free to keep using the value afterwards. Printing the result of the
// first print is what makes a clobbered return value visible.
func TestAsmRunPrintReturnsItsArgument(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	s := constStr(f, e, "twice")
	back := callPtrOp(f, e, "print", s)
	callOp(f, e, "print", back)
	f.SetRet(e, constOp(f, e, 0))

	stdout, _, _ := runModuleOutput(t, map[string]*ssa.Func{"main": f}, "main", 8)
	if stdout != "twice\ntwice\n" {
		t.Errorf("stdout = %q, want the string printed twice — print did not hand its argument back", stdout)
	}
}

// __alloc_reuse hands the token back when the size classes match: that is the
// whole point of the primitive, and a fresh block instead would be correct but
// would silently retire the optimisation.
func TestAsmRunAllocReuseInPlace(t *testing.T) {
	// tokenSize and size in the same 16-byte class -> ret == token.
	sameBlock := func(tokenSize, size int64) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		tok := allocOp(f, e, 24)
		got := callPtrOp(f, e, "__alloc_reuse", tok, constOp(f, e, tokenSize), constOp(f, e, size))
		f.SetRet(e, f.AddOp(e, ssa.OpEq, got, tok))
		return assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil)
	}
	if got := sameBlock(24, 24); got != 1 {
		t.Errorf("equal sizes: reused = %d, want 1 (the token itself)", got)
	}
	// 17..32 is one class, so a smaller request still fits the block.
	if got := sameBlock(32, 17); got != 1 {
		t.Errorf("same 16-byte class: reused = %d, want 1", got)
	}
	if got := sameBlock(24, 40); got != 0 {
		t.Errorf("class mismatch: reused = %d, want 0 (a fresh block)", got)
	}
}

// The fresh path is a real allocation: rc == 1 at [data-8], like MemAlloc, and
// the block does not overlap the token it declined to reuse.
func TestAsmRunAllocReuseFreshBlock(t *testing.T) {
	rcOf := func() int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		fresh := callPtrOp(f, e, "__alloc_reuse", constOp(f, e, 0), constOp(f, e, 0), constOp(f, e, 24))
		f.SetRet(e, loadMem(f, e, fresh, -8, ssa.OpLoad32U))
		return assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil)
	}
	if got := rcOf(); got != 1 {
		t.Errorf("fresh block's rc = %d, want 1", got)
	}

	// A null token allocates; the token's own bytes must survive a mismatched
	// reuse, since the block is still live for its owner.
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	tok := allocOp(f, e, 24)
	storeMem(f, e, tok, 0, constOp(f, e, 0x5a), ssa.OpStore)
	fresh := callPtrOp(f, e, "__alloc_reuse", tok, constOp(f, e, 24), constOp(f, e, 64))
	storeMem(f, e, fresh, 0, constOp(f, e, 0x17), ssa.OpStore)
	f.SetRet(e, loadMem(f, e, tok, 0, ssa.OpLoad))
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil); got != 0x5a {
		t.Errorf("token's word = %#x after a mismatched reuse wrote to the fresh block, want 0x5a — the blocks overlap", got)
	}
}

// __fern_drop_arr_str releases the ELEMENTS when the array is uniquely held.
// The observable is the element's own rc: a shared string (rc 2) comes back to
// 1, where a helper that skipped the walk would leave it at 2.
func TestAsmRunDropArrStrWalksElements(t *testing.T) {
	// arrRc is the count written into the array's header before the drop.
	elemRcAfterDrop := func(arrRc int64) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()

		// One heap string, given a second owner so the dec is visible.
		buf := callPtrOp(f, e, "__alloc_u8", constOp(f, e, 2))
		storeMem(f, e, buf, 0, constOp(f, e, 'h'), ssa.OpStore8)
		storeMem(f, e, buf, 1, constOp(f, e, 'i'), ssa.OpStore8)
		str := callPtrOp(f, e, "string_from_bytes_unchecked", buf)
		callOp(f, e, "__fern_rc_inc", str)

		// A one-element array of string pointers: __alloc_u8 lays down the
		// cap/rc/len header this helper reads, and the length is the ELEMENT
		// count, so it is rewritten to 1 over the 8 payload bytes.
		arr := callPtrOp(f, e, "__alloc_u8", constOp(f, e, 8))
		storeMem(f, e, arr, -4, constOp(f, e, 1), ssa.OpStore32)
		storeMem(f, e, arr, -8, constOp(f, e, arrRc), ssa.OpStore32)
		storeMem(f, e, arr, 0, str, ssa.OpStore)

		callOp(f, e, "__fern_drop_arr_str", arr, constOp(f, e, 8))
		f.SetRet(e, loadMem(f, e, str, -8, ssa.OpLoad32U))
		return assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil)
	}
	if got := elemRcAfterDrop(1); got != 1 {
		t.Errorf("element rc after dropping a UNIQUE array = %d, want 1 (2 = the walk did not run)", got)
	}
	if got := elemRcAfterDrop(2); got != 2 {
		t.Errorf("element rc after dropping a SHARED array = %d, want 2 — the other owner still reads those elements", got)
	}
}

// The array's own reference is dropped either way, which is what makes the
// helper a drop rather than a walk: a shared array comes back to rc 1.
func TestAsmRunDropArrStrDecsTheArray(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	arr := callPtrOp(f, e, "__alloc_u8", constOp(f, e, 8))
	storeMem(f, e, arr, -4, constOp(f, e, 0), ssa.OpStore32) // no elements to walk
	storeMem(f, e, arr, -8, constOp(f, e, 2), ssa.OpStore32) // shared
	callOp(f, e, "__fern_drop_arr_str", arr, constOp(f, e, 8))
	f.SetRet(e, loadMem(f, e, arr, -8, ssa.OpLoad32U))
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil); got != 1 {
		t.Errorf("array rc after the drop = %d, want 1", got)
	}
}

// A null or low address is not a heap array: the helper returns without
// touching it, the same guard every rc helper here carries.
func TestAsmRunDropArrStrGuardsLowAddresses(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	callOp(f, e, "__fern_drop_arr_str", constOp(f, e, 0), constOp(f, e, 8))
	callOp(f, e, "__fern_drop_arr_str", constOp(f, e, 8), constOp(f, e, 8))
	f.SetRet(e, constOp(f, e, 7))
	if got := assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil); got != 7 {
		t.Errorf("exit = %d, want 7 — a guarded drop must not fault", got)
	}
}
