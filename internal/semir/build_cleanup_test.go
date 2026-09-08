package semir

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

var cleanupActionCases = []struct{ name, source, want string }{
	{"cleanup-return-snapshot", `function pilot(): string {
  var items = ["saved"]; defer items = ["replacement"]; return items[0];
}`, "saved\n"},
	{"cleanup-late-read", `function pilot(): string {
  var items: i32[] = [2]; defer check(items[0]); items = [9]; return "late";
}
function check(n: i32): void { if (n != 9i32) { var empty: i32[] = []; var bad = empty[0]; } }`, "late\n"},
	{"cleanup-action-write", `function pilot(): string {
  var items: i32[] = [2]; defer check(items[0]); defer items = [9]; return "ordered";
}
function check(n: i32): void { if (n != 9i32) { var empty: i32[] = []; var bad = empty[0]; } }`, "ordered\n"},
	{"cleanup-no-captures", `function pilot(): string { defer sink(["discarded"]); return "empty env"; }
function sink(own items: string[]): void {}`, "empty env\n"},
	{"cleanup-void-tail", `function pilot(): string { work(); return "void"; }
function work(): void { var items = ["released"]; defer sink([items]); }
function sink(own items: string[][]): void {}`, "void\n"},
	{"cleanup-branch-action", `function pilot(): string {
  var items: i32[] = [2]; var enabled = true;
  defer check(items[0]); defer { if (enabled) { items = [9] } else { items = [3] } }
  return "branch";
}
function check(n: i32): void { if (n != 9i32) { var empty: i32[] = []; var bad = empty[0]; } }`, "branch\n"},
	{"cleanup-local-loop", `function pilot(): string {
  var items: i32[] = [0]; defer check(items[0]);
  defer { var i = 0i32; while (i < 3i32) { items = [i]; i = i + 1i32; } }
  return "loop";
}
function check(n: i32): void { if (n != 2i32) { var empty: i32[] = []; var bad = empty[0]; } }`, "loop\n"},
	{"cleanup-return-from-loop", `function pilot(): string {
  var items: i32[] = [2]; defer check(items[0]);
  loop { items = [9]; return "loop return"; }
}
function check(n: i32): void { if (n != 9i32) { var empty: i32[] = []; var bad = empty[0]; } }`, "loop return\n"},
	{"cleanup-multiple-returns", `function pilot(): string { return choose(true); }
function choose(flag: boolean): string {
  var items = ["branch"]; defer sink([items]);
  if (flag) { return items[0]; } return "other";
}
function sink(own items: string[][]): void {}`, "branch\n"},
	{"cleanup-return-before-register", `function pilot(): string { return choose(true); }
function choose(flag: boolean): string {
  if (flag) { return "early"; } defer fault(); return "later";
}
function fault(): void { var empty: i32[] = []; var bad = empty[0]; }`, "early\n"},
	{"cleanup-shadow-capture", `function pilot(): string {
  var items: i32[] = [9]; defer check(items[0]);
  defer { var items: i32[] = [2]; check(items[0] + 7i32); }
  return "shadow";
}
function check(n: i32): void { if (n != 9i32) { var empty: i32[] = []; var bad = empty[0]; } }`, "shadow\n"},
	{"cleanup-simultaneous-values", `function pilot(): string {
  var first: i32[] = [3]; var second: i32[] = [7]; defer check(first[0], second[0]);
  defer { var saved = first; first = second; second = saved; }
  return "swapped";
}
function check(a: i32, b: i32): void { if (a != 7i32 || b != 3i32) { var empty: i32[] = []; var bad = empty[0]; } }`, "swapped\n"},
	{"cleanup-captured-own-borrow", `function pilot(): string { return work(["anchored"]); }
function work(own items: string[]): string { var result = items[0]; defer inspect(items, items); return result; }
function inspect(reader: string[], own taken: string[]): void { var value = reader[0]; }`, "anchored\n"},
	{"cleanup-outer-loop-phi", `function pilot(): string {
  var items: i32[] = [0]; defer check(items[0]);
  var i = 0i32; while (i < 3i32) { items = [i]; i = i + 1i32; }
  return "last";
}
function check(n: i32): void { if (n != 2i32) { var empty: i32[] = []; var bad = empty[0]; } }`, "last\n"},
}

func TestBuildCleanupActions(t *testing.T) {
	for _, tc := range cleanupActionCases {
		t.Run(tc.name, func(t *testing.T) {
			prog, info := checkedProgram(t, tc.source)
			p, err := BuildProgram(prog, info)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := planProgramUnits(p); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func cleanupProgram(t *testing.T) *Program {
	t.Helper()
	prog, info := checkedProgram(t, `function pilot(flag: boolean): string {
  var items = ["retained"]; var count = 0i32;
  defer inspect(items, count); defer { items = ["replaced"]; count = 9i32; }
  if (flag) { return "yes"; } return "no";
}
function inspect(items: string[], count: i32): void { var value = items[0]; }`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCleanupRegionsKeepBindingsWithoutRuntimeEnvironments(t *testing.T) {
	p := cleanupProgram(t)
	f := p.funcs[0]
	if len(f.cleanups) != 2 || len(f.cleanups[0].replays) != 2 || len(f.cleanups[1].replays) != 2 {
		t.Fatal("actions must be built once and replayed on both returns")
	}
	for _, r := range f.cleanups {
		if len(r.captures) != 2 || r.captures[0] != f.cleanups[0].captures[0] || r.captures[1] != f.cleanups[0].captures[1] {
			t.Fatal("action interfaces lost shared lexical binding identities")
		}
		if r.pos.Line == 0 || r.body.values[r.body.graph.Params[0].ID].pos.Line == 0 {
			t.Fatal("lost action or capture source origin")
		}
	}
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			if op.Kind == ssa.OpTupleMake {
				t.Fatal("region protocol tuple escaped into the executable graph")
			}
		}
	}
}

func TestVerifyRejectsMalformedCleanupRegions(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Func, *cleanupRegion)
	}{
		{"owner", func(f *Func, r *cleanupRegion) { r.owner = r.body }},
		{"recursive-body", func(f *Func, r *cleanupRegion) { r.body = f }},
		{"foreign-program", func(f *Func, r *cleanupRegion) { r.body.program = &Program{} }},
		{"foreign-register", func(f *Func, r *cleanupRegion) { r.register = r.body.graph.Entry }},
		{"foreign-replay", func(f *Func, r *cleanupRegion) { r.replays[0] = r.body.graph.Entry }},
		{"duplicate-replay", func(f *Func, r *cleanupRegion) { r.replays = append(r.replays, r.replays[0]) }},
		{"missing-replay", func(f *Func, r *cleanupRegion) { r.replays = r.replays[1:] }},
		{"duplicate-capture", func(f *Func, r *cleanupRegion) { r.captures[1] = r.captures[0] }},
		{"foreign-capture", func(f *Func, r *cleanupRegion) { r.captures[0] = BindingID(len(f.bindings) + 1) }},
		{"counted-capture", func(f *Func, r *cleanupRegion) { r.body.modes[0] = ParamCounted }},
		{"binding-type", func(f *Func, r *cleanupRegion) { f.bindings[r.captures[0]-1].typ = ast.BoolType{} }},
		{"output-type", func(f *Func, r *cleanupRegion) { r.yield.Args[0], r.yield.Args[1] = r.yield.Args[1], r.yield.Args[0] }},
		{"yield", func(f *Func, r *cleanupRegion) { r.yield = nil }},
		{"source-origin", func(f *Func, r *cleanupRegion) { r.pos = ast.Position{} }},
		{"lifo", func(f *Func, r *cleanupRegion) { f.cleanups[0], f.cleanups[1] = f.cleanups[1], f.cleanups[0] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := cleanupProgram(t)
			f := p.funcs[0]
			tc.edit(f, f.cleanups[0])
			if err := VerifyProgram(p); err == nil {
				t.Fatal("malformed cleanup contract was accepted")
			}
		})
	}
}

func TestCleanupUnsupportedRegistration(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"conditional", `function pilot(flag: boolean): string { if (flag) { defer sink(); } return "x"; } function sink(): void {}`, "conditional cleanup"},
		{"iteration", `function pilot(): string { loop { defer sink(); break; } return "x"; } function sink(): void {}`, "iteration cleanup"},
		{"condition", `function pilot(): string { while ({ defer sink(); false }) {} return "x"; } function sink(): void {}`, "iteration cleanup"},
		{"nested", `function pilot(): string { defer { defer sink(); } return "x"; } function sink(): void {}`, "nested cleanup"},
		{"return", `function pilot(): string { defer { return "inside"; } return "x"; }`, "return inside a cleanup"},
		{"escaping-break", `function pilot(): string { outer: loop { defer { break outer; } } }`, "iteration cleanup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, info := checkedProgram(t, tc.source)
			_, err := BuildProgram(prog, info)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestARM64TypedCleanupKeepsObservableEffects(t *testing.T) {
	armLauncher(t)
	for _, optimize := range []bool{false, true} {
		out := lowerCheckedARM64(t, `function pilot(): string { defer fault(); return "cleanup erased"; }
function fault(): void { var empty: i32[] = []; var value = empty[0]; }`)
		stdout, stderr, code := runARM64Pilot(t, armExecutable(t, out, printHarness(out), optimize))
		if code != 134 || stdout != "" {
			t.Fatalf("optimized=%v: exit %d stdout %q stderr %q; expected bounds abort", optimize, code, stdout, stderr)
		}
	}
}

func BenchmarkTypedCleanupActions(b *testing.B) {
	for _, count := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("actions-%d", count), func(b *testing.B) {
			var source strings.Builder
			source.WriteString(`function pilot(items: string[][]): string[][] { var result = items;`)
			for i := 0; i < count; i++ {
				source.WriteString(`defer result = result.append(["cleanup"]);`)
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
