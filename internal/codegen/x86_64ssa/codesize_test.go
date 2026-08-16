package x86_64ssa_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	stackx86 "github.com/jakechampion/lang/internal/codegen/x86_64"
	x86 "github.com/jakechampion/lang/internal/codegen/x86_64ssa"
	"github.com/jakechampion/lang/internal/monomorph"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
	"github.com/jakechampion/lang/internal/parser"
)

// textSizes compiles src through both the stack-machine backend and the SSA
// register-allocating backend and returns each one's assembled .text byte count.
// Both go through the same in-process assembler, so the ratio is deterministic
// and environment-independent.
func textSizes(t *testing.T, src string) (ssaBytes, smBytes int) {
	t.Helper()
	p1, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	i1, err := checker.Check(p1)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	monomorph.Run(p1, i1)
	ssaAsm, err := x86.EmitProgram(p1, i1, 10)
	if err != nil {
		t.Fatalf("SSA EmitProgram: %v", err)
	}
	ssaText, _, err := nativex86.AssembleProgram(ssaAsm, nativeelf.TextVAddr)
	if err != nil {
		t.Fatalf("SSA AssembleProgram: %v", err)
	}
	p2, _ := parser.Parse(src)
	i2, _ := checker.Check(p2)
	monomorph.Run(p2, i2)
	smAsm, err := stackx86.Emit(p2, i2)
	if err != nil {
		t.Fatalf("stack-machine Emit: %v", err)
	}
	smText, _, err := nativex86.AssembleProgram(smAsm, nativeelf.TextVAddr)
	if err != nil {
		t.Fatalf("stack-machine AssembleProgram: %v", err)
	}
	return len(ssaText), len(smText)
}

// TestCodeSizeSmallerThanStackMachine is the emit-quality regression guard: the
// register-allocating SSA backend must keep emitting smaller code than the
// stack-machine backend it is meant to replace (the whole point of the
// binary-size epic, #4109/#4112). It catches a real emit-quality regression
// (e.g. losing the result-into-home coalescing or the call-clobber-aware
// caller-save). See docs/SSA-REGALLOC-PLAN.md for the phase results.
//
// Measured ratios: loop 51.8%, arith 46.8%, the 100-function mixed program
// 93.1%. The spread is the SHAPE MIX, not a scaling effect — genMixedProgram
// cycles four shapes, and at n=1 (pure arithmetic only) the ratio is 47.1%,
// rising to 89.2% at n=25 as the loop / nested-conditional / two-call-combiner
// shapes enter. So the large figure is not comparable with the two small ones
// and is not evidence that the SSA advantage decays with program size.
//
// THIS IS A RELATIVE MARGIN, so improving the stack machine narrows it with no
// SSA regression whatever. That is what moved the large case from 86.6% to
// 93.1%: the P3 dead-push peephole shrank the stack machine 25,829 -> 24,029
// bytes here while the SSA .text stayed byte-identical at 22,377. Re-measure
// both sides before reading a moved ratio as an SSA problem — the numbers
// above are stated so the next mover can.
func TestCodeSizeSmallerThanStackMachine(t *testing.T) {
	// Straight-line and loop code — where register allocation wins clearly.
	local := map[string]string{
		"arith": `function f(a: i32, b: i32, c: i32): i32 { return (a*b + c) * (a - b) + c*c - (a+b+c); } function main(): i32 { return f(3, 5, 7); }`,
		"loop":  `function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 1000) { t = t + i*3 - 1; i = i + 1; } return t; }`,
	}
	for name, src := range local {
		ssaB, smB := textSizes(t, src)
		if ssaB >= smB {
			t.Errorf("%s: SSA .text=%d not smaller than stack machine=%d (%.0f%%)", name, ssaB, smB, float64(ssaB)/float64(smB)*100)
		}
	}

	// A large program of many varied functions — the shape a real codebase has.
	// Measured 93.1%; the bound leaves ~2pp, which is tighter in absolute terms
	// than the 90% it replaces (that one tolerated 7pp of SSA growth over its
	// own 84% measurement). Both sides are deterministic byte counts from the
	// in-process assembler, so a tight bound cannot flake.
	ssaB, smB := textSizes(t, genMixedProgram(100))
	ratio := float64(ssaB) / float64(smB)
	if ratio > 0.95 {
		t.Errorf("large program: SSA .text=%d is %.0f%% of stack machine=%d, want <=95%%", ssaB, ratio*100, smB)
	}
	t.Logf("large (100 fns): SSA .text=%d  stack-machine=%d  (SSA %.0f%% of SM)", ssaB, smB, ratio*100)
}

// genMixedProgram builds a program of n functions in four recurring shapes
// (arithmetic, a counted loop, nested conditionals, and a two-call combiner),
// plus a main that sums a call to each — a representative spread of control flow.
func genMixedProgram(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		switch i % 4 {
		case 0:
			fmt.Fprintf(&b, "function fn%d(a: i32): i32 { var b: i32 = a + 3; var c: i32 = a - 1; return (a*b + c) * (a - b) + c*c - (a+b+c); }\n", i)
		case 1:
			fmt.Fprintf(&b, "function fn%d(n: i32): i32 { var t: i32 = 0; var i: i32 = 0; while (i < n) { t = t + i*3 - 1; i = i + 1; } return t; }\n", i)
		case 2:
			fmt.Fprintf(&b, "function fn%d(x: i32): i32 { if (x < 10) { return x*x; } if (x < 100) { return x + 7; } return x - 3; }\n", i)
		case 3:
			fmt.Fprintf(&b, "function fn%d(x: i32): i32 { return fn%d(x) + fn%d(x); }\n", i, i-1, i-2)
		}
	}
	b.WriteString("function main(): i32 { var s: i32 = 0;\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "  s = s + fn%d(%d);\n", i, i%20)
	}
	b.WriteString("  return s; }\n")
	return b.String()
}
