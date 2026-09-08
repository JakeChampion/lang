package semir

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

var sourceLoopCases = []struct{ name, source, want string }{
	{"source-loop-mixed-break-taken", `function pilot(): string { return choose(true); }
function choose(jump: boolean): string {
  var items = ["initial"]; var i = 0i32;
  outer: while (i < 2i32) {
    i = i + 1i32; var j = 0i32;
    while (j < 2i32) {
      j = j + 1i32;
      if (jump) { items = ["outer break"]; break outer; }
      items = ["inner body"];
    }
    items = ["normal exit"];
  }
  return items[0];
}`, "outer break\n"},
	{"source-loop-mixed-break-normal", `function pilot(): string { return choose(false); }
function choose(jump: boolean): string {
  var items = ["initial"]; var i = 0i32;
  outer: while (i < 2i32) {
    i = i + 1i32; var j = 0i32;
    while (j < 2i32) {
      j = j + 1i32;
      if (jump) { items = ["outer break"]; break outer; }
      items = ["inner body"];
    }
    items = ["normal exit"];
  }
  return items[0];
}`, "normal exit\n"},
	{"source-loop-mixed-continue-taken", `function pilot(): string { return choose(true); }
function choose(jump: boolean): string {
  var items = ["initial"]; var i = 0i32;
  outer: while (i < 2i32) {
    i = i + 1i32; var j = 0i32;
    while (j < 2i32) {
      j = j + 1i32;
      if (jump) { items = ["outer continue"]; continue outer; }
      items = ["inner body"];
    }
    items = ["normal exit"];
  }
  return items[0];
}`, "outer continue\n"},
	{"source-loop-mixed-continue-normal", `function pilot(): string { return choose(false); }
function choose(jump: boolean): string {
  var items = ["initial"]; var i = 0i32;
  outer: while (i < 2i32) {
    i = i + 1i32; var j = 0i32;
    while (j < 2i32) {
      j = j + 1i32;
      if (jump) { items = ["outer continue"]; continue outer; }
      items = ["inner body"];
    }
    items = ["normal exit"];
  }
  return items[0];
}`, "normal exit\n"},
	{"source-loop-unconditional-outer-break", `function pilot(): string {
  var items = ["initial"];
  outer: loop {
    loop { items = ["outer exit"]; break outer; }
    items = ["dead after inner"];
  }
  return items[0];
}`, "outer exit\n"},
	{"source-loop-unconditional-outer-continue", `function pilot(): string {
  var items = ["initial"]; var i = 0i32;
  outer: while (i < 2i32) {
    i = i + 1i32;
    loop { items = ["outer continue"]; continue outer; }
    items = ["dead after inner"];
  }
  return items[0];
}`, "outer continue\n"},
	{"source-loop-append-snapshot", `function pilot(): string {
  var items = [["original"]]; var saved = items;
  var i = 0i32;
  while (i < 64i32) { items = items.append(["new"]); i = i + 1i32; }
  if (i != 64i32) { return "wrong count"; }
  var last = items[64i32][0]; return saved[0i32][0];
}`, "original\n"},
	{"source-loop-appended-child", `function pilot(): string {
  var items = [["original"]]; var i = 0i32;
  while (i < 64i32) { items = items.append(["new"]); i = i + 1i32; }
  return items[64i32][0];
}`, "new\n"},
	{"source-loop-zero", `function pilot(): string {
  var items = ["initial"]; var i = 0i32;
  while (i < 0i32) { items = ["wrong"]; i = i + 1i32; }
  return items[0];
}`, "initial\n"},
	{"source-loop-break", `function pilot(): string {
  var items = ["initial"]; var i = 0i32;
  while (i < 20i32) { items = ["break"]; if (i == 3i32) { break; } i = i + 1i32; }
  if (i != 3i32) { return "wrong count"; } return items[0];
}`, "break\n"},
	{"source-loop-continue", `function pilot(): string {
  var items = ["initial"]; var i = 0i32;
  while (i < 20i32) {
    var temporary = [["temporary"]]; i = i + 1i32;
    if (i < 20i32) { continue; }
    items = temporary[0];
  }
  return items[0];
}`, "temporary\n"},
	{"source-loop-nested-label-break", `function pilot(): string {
  var items = ["initial"]; var i = 0i32;
  outer: while (i < 8i32) {
    var j = 0i32;
    while (j < 8i32) { items = ["outer break"]; break outer; }
    items = ["wrong"];
  }
  return items[0];
}`, "outer break\n"},
	{"source-loop-nested-label-continue", `function pilot(): string {
  var items = ["initial"]; var i = 0i32;
  outer: while (i < 8i32) {
    i = i + 1i32; var j = 0i32;
    while (j < 8i32) { items = ["outer continue"]; continue outer; }
    items = ["wrong"];
  }
  return items[0];
}`, "outer continue\n"},
	{"source-loop-unconditional", `function pilot(): string {
  var items = ["initial"]; var i = 0i32;
  loop { items = ["loop"]; i = i + 1i32; if (i == 8i32) { break; } }
  return items[0];
}`, "loop\n"},
	{"source-loop-early-return", `function pilot(): string {
  var items = [["initial"]];
  loop { var replacement = [["early"]]; return replacement[0][0]; }
}`, "early\n"},
	{"source-loop-shadow", `function pilot(): string {
  var items = ["outer"]; var i = 0i32;
  while (i < 8i32) { var items = ["inner"]; items = ["changed inner"]; i = i + 1i32; }
  return items[0];
}`, "outer\n"},
	{"source-loop-projection-anchor", `function pilot(): string {
  var items = [["anchored"]]; var child = items[0]; var i = 0i32;
  while (i < 64i32) { items = [["churn"]]; i = i + 1i32; }
  return child[0];
}`, "anchored\n"},
	{"source-loop-simultaneous-swap", `function pilot(): string {
  var a = ["left"]; var b = ["right"]; var i = 0i32;
  while (i < 31i32) { var previous = a; a = b; b = previous; i = i + 1i32; }
  return a[0];
}`, "right\n"},
	{"source-loop-counted-parameter", `function pilot(): string { return grow([["first"]], 8i32)[8][0]; }
function grow(own items: string[][], count: i32): string[][] {
  var result = items; var i = 0i32;
  while (i < count) { result = result.append(["counted"]); i = i + 1i32; }
  return result;
}`, "counted\n"},
	{"source-loop-tuple-replacement", `function pilot(): string {
  var pair = (["initial"], 0i32); var i = 0i32;
  while (i < 8i32) { pair = (["tuple"], i); i = i + 1i32; }
  let (items, count) = pair; if (count != 7i32) { return "wrong count"; }
  return items[0];
}`, "tuple\n"},
	{"source-branch-binding-phi", `function pilot(): string { return choose(true)[0]; }
function choose(yes: boolean): string[] {
  var items = ["before"]; if (yes) { items = ["then"]; } else { items = ["else"]; }
  return items;
}`, "then\n"},
	{"source-branch-binding-phi-else", `function pilot(): string { return choose(false)[0]; }
function choose(yes: boolean): string[] {
  var items = ["before"]; if (yes) { items = ["then"]; } else { items = ["else"]; }
  return items;
}`, "else\n"},
	{"source-i32-wrapping", `function pilot(): string {
  var max = 2147483647i32; var wrapped = max + 1i32;
  if (wrapped < 0i32) { return "wrapped"; } return "wrong";
}`, "wrapped\n"},
}

func TestSourceLoopBuildsVerifiedUnitFlow(t *testing.T) {
	for _, tc := range sourceLoopCases {
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
		})
	}
}

func TestSourceLoopKeepsUnchangedBorrowIdentity(t *testing.T) {
	prog, info := checkedProgram(t, `function pilot(items: string[], running: boolean): string[] {
  while (running) { running = false; } return items;
}`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	f := p.funcs[0]
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			if op.Kind == ssa.OpPhi && referenceBearing(f.values[op.Result.ID].typ) {
				t.Fatal("unchanged borrowed array acquired a spurious loop phi")
			}
		}
	}
}

func TestSourceLoopDoesNotResurrectUnconditionalExit(t *testing.T) {
	for _, tc := range sourceLoopCases {
		if !strings.HasPrefix(tc.name, "source-loop-unconditional-outer-") {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			prog, info := checkedProgram(t, tc.source)
			p, err := BuildProgram(prog, info)
			if err != nil {
				t.Fatal(err)
			}
			for _, fn := range p.funcs {
				for _, block := range fn.graph.Blocks {
					if block != fn.graph.Entry && len(block.Preds) == 0 {
						t.Fatal("retained a disconnected loop exit")
					}
					for _, op := range block.Ops {
						if op.Kind == ssa.OpConstString && op.Str == "dead after inner" {
							t.Fatal("emitted statements after an unconditional outer jump")
						}
					}
				}
			}
		})
	}
}

// Measures the typed producer plus independently verified physical lowering,
// not parsing/checking, machine optimization, assembly or generated runtime.
func BenchmarkTypedSourceLoops(b *testing.B) {
	for _, count := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("loops-%d", count), func(b *testing.B) {
			var source strings.Builder
			source.WriteString(`function pilot(items: string[][]): string[][] { var result = items;`)
			for i := 0; i < count; i++ {
				fmt.Fprintf(&source, `var counter%d = 0i32; while (counter%d < 8i32) { result = result.append(["new"]); counter%d = counter%d + 1i32; }`, i, i, i, i)
			}
			source.WriteString(`return result; }`)
			prog, info := checkedProgram(b, source.String())
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p, err := BuildProgram(prog, info)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := LowerARM64SSA(p); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
