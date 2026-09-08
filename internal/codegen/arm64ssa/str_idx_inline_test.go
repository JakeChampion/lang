package arm64ssa_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

func stringIndexModule(text string, index int64) map[string]*ssa.Func {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	p := constStr(f, b, text)
	addr := addrCallOp(f, b, "__str_idx", p, constOp(f, b, index))
	f.SetRet(b, load8u(f, b, addr, 0))
	return map[string]*ssa.Func{"main": f}
}

func TestStringIndexIsInlinedNotCalled(t *testing.T) {
	asm := emitIdxAsm(t, stringIndexModule("abc", 1), "main")
	if strings.Contains(asm, "bl fn___str_idx") {
		t.Error("string index still emitted as a call")
	}
	for _, want := range []string{".Lssa_idx_", "ldur", "cmp", "b.lo", "#134"} {
		if !strings.Contains(asm, want) {
			t.Errorf("inlined string index is missing %q", want)
		}
	}
}

func TestInlinedStringIndexPreservesBytesAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		text  string
		index int64
		want  int
	}{
		{name: "first", text: "abc", index: 0, want: 97},
		{name: "last", text: "abc", index: 2, want: 99},
		{name: "nul", text: "a\x00b", index: 1, want: 0},
		{name: "high", text: "a\xffb", index: 1, want: 255},
		{name: "empty", text: "", index: 0, want: 134},
		{name: "negative", text: "abc", index: -1, want: 134},
		{name: "minimum-i32", text: "abc", index: -2147483648, want: 134},
		{name: "past-end", text: "abc", index: 3, want: 134},
		{name: "maximum-i32", text: "abc", index: 2147483647, want: 134},
	} {
		for _, regs := range []int{2, 4, arm64ssa.DefaultNumAlloc} {
			t.Run(tc.name+"/"+strconv.Itoa(regs), func(t *testing.T) {
				if got := assembleRunArmModule(t, stringIndexModule(tc.text, tc.index), "main", regs); got != tc.want {
					t.Errorf("string byte at %d = %d, want %d", tc.index, got, tc.want)
				}
			})
		}
	}
}

func TestInlinedStringIndexesHaveDistinctLabels(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	p := constStr(f, b, "AB")
	a := load8u(f, b, addrCallOp(f, b, "__str_idx", p, constOp(f, b, 0)), 0)
	c := load8u(f, b, addrCallOp(f, b, "__str_idx", p, constOp(f, b, 1)), 0)
	f.SetRet(b, f.AddOp(b, ssa.OpAdd, a, c))
	for _, regs := range []int{2, 4, arm64ssa.DefaultNumAlloc} {
		if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", regs); got != 131 {
			t.Errorf("two string indexes with %d registers = %d, want 131", regs, got)
		}
	}
}
