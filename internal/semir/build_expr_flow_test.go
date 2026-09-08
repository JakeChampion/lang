package semir

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

var expressionFlowCases = []struct{ name, source, want string }{
	{"expression-condition-break-enclosing-loop", `function pilot(): string {
  var items = ["before"];
  loop {
    while (if (true) { items = ["outer break"]; break; } else { false }) {}
    return "wrong loop";
  }
  return items[0];
}`, "outer break\n"},
	{"expression-condition-continue-enclosing-loop", `function pilot(): string {
  var items = ["before"]; var i = 0i32;
  while (i < 2i32) {
    while (if (i < 2i32) { i = i + 1i32; items = ["outer continue"]; continue; } else { false }) {}
    return "wrong loop";
  }
  return items[0];
}`, "outer continue\n"},
	{"expression-condition-labeled-break", `function pilot(): string {
  var items = ["before"];
  outer: loop {
    inner: while (if (true) { items = ["labeled break"]; break outer; } else { false }) {}
    return "wrong loop";
  }
  return items[0];
}`, "labeled break\n"},
	{"expression-condition-labeled-continue", `function pilot(): string {
  var items = ["before"]; var i = 0i32;
  outer: while (i < 2i32) {
    inner: while (if (i < 2i32) { i = i + 1i32; items = ["labeled continue"]; continue outer; } else { false }) {}
    return "wrong loop";
  }
  return items[0];
}`, "labeled continue\n"},
	{"expression-and-skips-fault", `function pilot(): string { return choose(false); }
function choose(flag: boolean): string { if (flag && fault()) { return "wrong"; } return "skipped"; }
function fault(): boolean { var missing: boolean[] = []; return missing[0]; }`, "skipped\n"},
	{"expression-or-skips-fault", `function pilot(): string { return choose(true); }
function choose(flag: boolean): string { if (flag || fault()) { return "skipped"; } return "wrong"; }
function fault(): boolean { var missing: boolean[] = []; return missing[0]; }`, "skipped\n"},
	{"expression-and-evaluates-rhs", `function pilot(): string {
  var items = ["before"];
  if (true && { items = ["rhs"]; true }) { return items[0]; }
  return "wrong";
}`, "rhs\n"},
	{"expression-or-evaluates-rhs", `function pilot(): string {
  var items = ["before"];
  if (false || { items = ["rhs"]; true }) { return items[0]; }
  return "wrong";
}`, "rhs\n"},
	{"expression-nested-short-circuit", `function pilot(): string {
  var a = false; var b = true;
  if (!(a && fault()) && (b || fault())) { return "nested"; } return "wrong";
}
function fault(): boolean { var missing: boolean[] = []; return missing[0]; }`, "nested\n"},
	{"expression-borrowed-join", `function pilot(): string { return choose(true); }
function choose(flag: boolean): string {
  var child = if (flag) { var parent = [["left"]]; parent[0] }
                     else { var parent = [["right"]]; parent[0] };
  var i = 0i32; while (i < 64i32) { var churn = [["churn"]]; i = i + 1i32; }
  return child[0];
}`, "left\n"},
	{"expression-borrowed-join-else", `function pilot(): string { return choose(false); }
function choose(flag: boolean): string {
  var child = if (flag) { var parent = [["left"]]; parent[0] }
                     else { var parent = [["right"]]; parent[0] };
  return child[0];
}`, "right\n"},
	{"expression-one-arm-returns", `function pilot(): string { return choose(false); }
function choose(flag: boolean): string {
  var items = if (flag) { return "early"; } else { ["live"] };
  return items[0];
}`, "live\n"},
	{"expression-returning-arm", `function pilot(): string { return choose(true); }
function choose(flag: boolean): string {
  var items = if (flag) { return "early"; } else { ["live"] };
  return items[0];
}`, "early\n"},
	{"expression-all-arms-return", `function pilot(): string { return choose(true); }
function choose(flag: boolean): string {
  return if (flag) { return "both"; } else { return "other"; };
}`, "both\n"},
	{"expression-call-argument-exits", `function pilot(): string {
  return take(["evaluated"], { return "argument"; }, fault());
}
function take(a: string[], b: string[], c: string[]): string { return "wrong"; }
function fault(): string[] { var missing: string[][] = []; return missing[0]; }`, "argument\n"},
	{"expression-partial-argument-exit", `function pilot(): string { return choose(true); }
function choose(flag: boolean): string {
  return take(["evaluated"], if (flag) { return "partial"; } else { ["live"] }, fault());
}
function take(a: string[], b: string[], c: string[]): string { return "wrong"; }
function fault(): string[] { var missing: string[][] = []; return missing[0]; }`, "partial\n"},
	{"expression-array-element-exits", `function pilot(): string {
  var items: string[][] = [["evaluated"], { return "array"; }, fault()];
  return items[0][0];
}
function fault(): string[] { var missing: string[][] = []; return missing[0]; }`, "array\n"},
	{"expression-append-item-exits", `function pilot(): string {
  var items = ["before"]; var result = items.append({ return "append"; }); return result[0];
}`, "append\n"},
	{"expression-index-exits", `function pilot(): string { return choose(true); }
function choose(flag: boolean): string {
  var items = ["before"]; return items[if (flag) { return "index"; } else { 0i32 }];
}`, "index\n"},
	{"expression-replacement-exits", `function pilot(): string { return choose(true); }
function choose(flag: boolean): string {
  var items = ["before"]; items = if (flag) { return items[0]; } else { ["after"] };
  return items[0];
}`, "before\n"},
	{"expression-tuple-field-exits", `function pilot(): string {
  var pair = (["evaluated"], { return "tuple"; }); return "wrong";
}`, "tuple\n"},
	{"expression-loop-break", `function pilot(): string {
  var items = ["initial"];
  outer: loop {
    var skipped: string[][] = [["evaluated"], { items = ["break"]; break outer; }];
    return "wrong";
  }
  return items[0];
}`, "break\n"},
	{"expression-loop-continue", `function pilot(): string {
  var items = ["initial"]; var i = 0i32;
  outer: while (i < 8i32) {
    i = i + 1i32;
    var skipped: string[][] = [["evaluated"], { items = ["continue"]; continue outer; }];
    return "wrong";
  }
  return items[0];
}`, "continue\n"},
	{"expression-loop-condition-exits", `function pilot(): string { return choose(true); }
function choose(flag: boolean): string {
  while (if (flag) { return "condition"; } else { false }) { var unused = ["wrong"]; }
  return "wrong";
}`, "condition\n"},
	{"expression-loop-condition-live", `function pilot(): string { return choose(false); }
function choose(flag: boolean): string {
  while (if (flag) { return "wrong"; } else { false }) { var unused = ["wrong"]; }
  return "live condition";
}`, "live condition\n"},
}

func TestARM64TypedShortCircuitTruthTable(t *testing.T) {
	armLauncher(t)
	for _, op := range []string{"&&", "||"} {
		for _, left := range []bool{false, true} {
			for _, right := range []bool{false, true} {
				for _, optimize := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%t/%t/optimized-%t", op, left, right, optimize), func(t *testing.T) {
						out := lowerCheckedARM64(t, fmt.Sprintf(`
function pilot(): string { return choose(%t, %t); }
function choose(left: boolean, right: boolean): string {
  var items = ["skipped"];
  var value = left %s { items = ["evaluated"]; right };
  if (value) { return "true"; } return items[0];
}`, left, right, op))
						value, evaluated := left && right, left
						if op == "||" {
							value, evaluated = left || right, !left
						}
						want := "skipped\n"
						if evaluated {
							want = "evaluated\n"
						}
						if value {
							want = "true\n"
						}
						stdout, stderr, code := runARM64Pilot(t, armExecutable(t, out, printHarness(out), optimize))
						if code != 0 || stdout != want {
							t.Fatalf("exit %d, stdout %q, stderr %q; want %q", code, stdout, stderr, want)
						}
						requireBalancedCensus(t, stderr)
					})
				}
			}
		}
	}
}

func TestExpressionFlowUsesOnlyLiveValues(t *testing.T) {
	for _, tc := range expressionFlowCases {
		t.Run(tc.name, func(t *testing.T) {
			prog, info := checkedProgram(t, tc.source)
			p, err := BuildProgram(prog, info)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := planProgramUnits(p); err != nil {
				t.Fatal(err)
			}
			if _, err := solveReturnFlow(p); err != nil {
				t.Fatal(err)
			}
			if tc.name == "expression-call-argument-exits" {
				for _, block := range p.funcs[0].graph.Blocks {
					for _, op := range block.Ops {
						if op.Kind == ssa.OpSemanticCall {
							t.Fatal("emitted call after its argument terminated")
						}
					}
				}
			}
		})
	}
}
