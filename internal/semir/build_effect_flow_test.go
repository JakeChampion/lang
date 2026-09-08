package semir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

var effectFlowCases = []struct{ name, source, want string }{
	{"effect-block-return", `function pilot(): string { action(); return "block"; }
function action(): void { return { var items = [["temporary"]]; inspect(items[0]) }; }
function inspect(items: string[]): void { var item = items[0]; }`, "block\n"},
	{"effect-block-scope", `function pilot(): string {
  var items = ["outer"]; ({ var items = ["inner"]; inspect(items) }); return items[0];
}
function inspect(items: string[]): void { var item = items[0]; }`, "outer\n"},
	{"effect-if-true", `function pilot(): string { return choose(true); }
function choose(flag: boolean): string {
  var items = ["before"]; (if (flag) { items = ["yes"]; noop() } else { items = ["no"]; noop() }); return items[0];
}
function noop(): void {}`, "yes\n"},
	{"effect-if-false", `function pilot(): string { return choose(false); }
function choose(flag: boolean): string {
  var items = ["before"]; (if (flag) { items = ["yes"]; noop() } else { items = ["no"]; noop() }); return items[0];
}
function noop(): void {}`, "no\n"},
	{"effect-if-skipped-fault", `function pilot(): string { action(true); return "skipped"; }
function action(flag: boolean): void { return if (flag) { noop() } else { fault() }; }
function noop(): void {}
function fault(): void { var empty: i32[] = []; var bad = empty[0]; }`, "skipped\n"},
	{"effect-if-arm-return", `function pilot(): string {
  (if (true) { return "arm exit"; } else { noop() }); return "wrong";
}
function noop(): void {}`, "arm exit\n"},
	{"effect-if-condition-return", `function pilot(): string {
  (if ({ return "condition exit"; true }) { noop() } else { noop() }); return "wrong";
}
function noop(): void {}`, "condition exit\n"},
	{"effect-if-all-return", `function pilot(): string {
  (if (false) { return "wrong"; } else { return "all exit"; }); return "dead";
}`, "all exit\n"},
	{"effect-match-guard-state", `function pilot(): string {
  var items = ["before"];
  (match (1i32) { 1i32 when { items = ["guard"]; false } => noop(), _ => noop() }); return items[0];
}
function noop(): void {}`, "guard\n"},
	{"effect-match-tuple-escape", `function pilot(): string {
  var held = ["before"];
  (match ((true, (["held"], 1i32))) { (true, (child, _)) => { held = child; noop() }, _ => noop() });
  var i = 0i32; while (i < 64i32) { var churn = ((["churn"], false), 2i32); i = i + 1i32; }
  return held[0];
}
function noop(): void {}`, "held\n"},
	{"effect-match-scrutinee-once", `function pilot(): string {
  var count = 0i32;
  (match ({ count = count + 1i32; count }) { 0i32 => noop(), 1i32 => noop(), _ => noop() });
  if (count == 1i32) { return "once"; } return "wrong";
}
function noop(): void {}`, "once\n"},
	{"effect-match-guard-exit", `function pilot(): string {
  (match ((["guard exit"], true)) { (items, _) when { return items[0]; true } => noop(), _ => noop() }); return "dead";
}
function noop(): void {}`, "guard exit\n"},
	{"effect-match-original-scrutinee", `function pilot(): string {
  var pair = (["old"], true);
  (match (pair) {
    (items, _) when { pair = (["new"], false); false } => inspect(items),
    (items, _) => inspect(items)
  });
  let (items, _) = pair; return items[0];
}
function inspect(items: string[]): void { var item = items[0]; }`, "new\n"},
	{"effect-match-continue", `function pilot(): string {
  var i = 0i32; var items = ["before"];
  while (i < 2i32) {
    i = i + 1i32;
    (match (i) { 1i32 => { items = ["continued"]; continue; }, _ => noop() });
    return items[0];
  }
  return "wrong";
}
function noop(): void {}`, "continued\n"},
	{"effect-match-void-return", `function pilot(): string { action((true, ["allocated"])); return "returned"; }
function action(pair: (boolean, string[])): void {
  return match (pair) { (true, items) => inspect(items), _ => noop() };
}
function inspect(items: string[]): void { var item = items[0]; }
function noop(): void {}`, "returned\n"},
	{"effect-discard-aggregate-if", `function pilot(): string {
  var i = 0i32;
  while (i < 64i32) { (if (i == 0i32) { [["first"]] } else { [["later"]] }); i = i + 1i32; }
  return "discarded";
}`, "discarded\n"},
	{"effect-discard-aggregate-match", `function pilot(): string {
  var i = 0i32;
  while (i < 64i32) { (match ((i, true)) { (0i32, _) => [["first"]], _ => [["later"]] }); i = i + 1i32; }
  return "discarded";
}`, "discarded\n"},
}

func TestBuildEffectControlFlow(t *testing.T) {
	for _, tc := range effectFlowCases {
		t.Run(tc.name, func(t *testing.T) {
			prog, info := checkedProgram(t, tc.source)
			p, err := BuildProgram(prog, info)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := planProgramUnits(p); err != nil {
				t.Fatal(err)
			}
			for _, f := range p.funcs {
				if _, fake := f.values[0]; fake {
					t.Fatal("effect control flow invented value zero")
				}
				for _, info := range f.values {
					if _, void := info.typ.(ast.VoidType); void {
						t.Fatal("effect control flow invented a void value")
					}
				}
			}
		})
	}
}

func TestDiscardedArmResultsDoNotCreatePhis(t *testing.T) {
	for _, source := range []string{
		`function pilot(flag: boolean): string { (if (flag) { [["yes"]] } else { [["no"]] }); return "done"; }`,
		`function pilot(flag: boolean): string { (match (flag) { true => [["yes"]], _ => [["no"]] }); return "done"; }`,
	} {
		prog, info := checkedProgram(t, source)
		p, err := BuildProgram(prog, info)
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range p.funcs[0].graph.Blocks {
			for _, op := range block.Ops {
				if op.Kind == ssa.OpPhi {
					t.Fatal("discarded arm results acquired an unnecessary result phi")
				}
			}
		}
	}
}

func TestValueJoinRejectsEffectResults(t *testing.T) {
	for _, values := range [][]ssa.Value{nil, {{}}} {
		f := newFunc("invalid_join", ast.StringType{})
		block := f.graph.NewBlock()
		b := builder{fn: f, current: block}
		if _, err := b.joinValues([]*ssa.Block{block}, values, ast.Position{}); err == nil || !strings.Contains(err.Error(), "value join") {
			t.Fatalf("value join accepted a missing result: %v", err)
		}
	}
}
