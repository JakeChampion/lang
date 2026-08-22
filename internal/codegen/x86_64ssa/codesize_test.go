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

// TestCodeSizeSmallerThanStackMachine is the emit-quality regression guard for
// the register-allocating SSA backend (#4109/#4112) — it catches a real emit
// regression such as losing the result-into-home coalescing or the
// call-clobber-aware caller-save. See docs/SSA-REGALLOC-PLAN.md.
//
// On straight-line arithmetic and a counted loop the SSA backend emits
// materially less code than the stack machine: measured arith 79.0%, loop 44.5%.
// Those legs stay a cross-backend comparison.
//
// They are also both ONE-FUNCTION programs, which is the whole reason the large
// leg below is pinned differently. A ratio at n=1 is dominated by fixed overhead
// and does not generalise: the two backends differ by a constant per function,
// so the same reduce shape measures 52.8% of the stack machine at n=1, 102.2% at
// n=10 and 139.2% at n=100. Only a slope shows that, which is what
// TestCodeSizeMarginalPerFunction measures.
//
// The large mixed program is pinned to the SSA backend's own deterministic byte
// count rather than to a ratio, because a cross-backend bound there would track
// the stack machine's packed 8-byte operand stack (#4111) as much as anything
// this backend does. Both sides come from the in-process assembler, so a tight
// tolerance cannot flake; widen it only with a re-measurement.
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
	//
	// This number went UP when EmitProgram started running ssa.Optimize +
	// ssa.Verify, the pipeline the shipping SSA backends run (#6979). That is
	// not an emit regression: 17761 was what this path produced with no
	// optimiser, which is a configuration no user can invoke. Optimize costs
	// 2.4% on this program — LICM hoisting lengthening live ranges into more
	// spills is the obvious suspect and is not yet verified — and the honest
	// figure for the shipping pipeline is the larger one.
	const ssaLargeText = 18179 // measured with Optimize+Verify (#6979); deterministic per commit
	ssaB, smB := textSizes(t, genMixedProgram(100))
	if grew := float64(ssaB)/float64(ssaLargeText) - 1; grew > 0.02 {
		t.Errorf("large program: SSA .text=%d is %.1f%% above the pinned %d — emit-quality regression?",
			ssaB, grew*100, ssaLargeText)
	}
	t.Logf("large (100 fns): SSA .text=%d (pinned %d)  stack-machine=%d  (SSA %.0f%% of SM)",
		ssaB, ssaLargeText, smB, float64(ssaB)/float64(smB)*100)
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
