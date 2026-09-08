package semir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/ssa"
)

func checkedFunc(t *testing.T, source string) (*ast.FuncDecl, *checker.Info) {
	t.Helper()
	prog, err := parser.Parse(source + "\nfunction main(): i32 { return 0; }")
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range prog.Funcs {
		if fn.Name == "pilot" {
			return fn, info
		}
	}
	t.Fatal("pilot declaration missing")
	return nil, nil
}

func TestBuildCheckedProjections(t *testing.T) {
	for _, tc := range []struct {
		name, source            string
		arrays, tuples, returns int
	}{
		{"parameter", `function pilot(items: string[]): string { return items[0]; }`, 1, 0, 1},
		{"alias", `function pilot(items: string[]): string { var alias = items; return alias[0]; }`, 1, 0, 1},
		{"constructed", `function pilot(): string { var items = ["aa!", "bb!"]; return items[1]; }`, 1, 0, 1},
		// The parser gives discards ordinary synthetic binding identities.
		// Preserve those field projections too; liveness, not name spelling,
		// decides which values can be eliminated after ownership analysis.
		{"tuple-binder", `function pilot(pair: (string[], i64)): string { let (items, _) = pair; return items[0]; }`, 1, 2, 1},
		{"nested-tuple", `function pilot(pair: ((string[], i64), boolean)): string { let ((items, _), _) = pair; return items[0]; }`, 1, 4, 1},
		{"tuple-constructor", `function pilot(items: string[]): string { let (alias, _) = (items, 9i64); return alias[0]; }`, 1, 2, 1},
		{"branch", `function pilot(items: string[], other: string[], choose: boolean): string { if (choose) { return items[0]; } else { return other[0]; } }`, 2, 0, 2},
		{"early-return", `function pilot(items: string[], choose: boolean): string { if (choose) { return "early"; } return items[0]; }`, 1, 0, 2},
		{"empty-array", `function pilot(): string[] { var items: string[] = []; return items; }`, 0, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decl, info := checkedFunc(t, tc.source)
			f, err := BuildFunc(decl, info)
			if err != nil {
				t.Fatal(err)
			}
			arrays, tuples, returns := 0, 0, 0
			uses := ssa.BuildUses(f.graph)
			for _, block := range f.graph.Blocks {
				if block.Term.Kind == ssa.TermRet {
					returns++
				}
				for _, op := range block.Ops {
					if op.Kind == ssa.OpArrayGet {
						arrays++
					}
					if op.Kind == ssa.OpTupleGet {
						tuples++
					}
					if f.values[op.Result.ID].pos.Line == 0 {
						t.Fatal("source position lost")
					}
					if p, ok := projection(op); ok && uses.Count(p.Container) == 0 {
						t.Fatal("projection container missing from def-use")
					}
				}
			}
			if arrays != tc.arrays || tuples != tc.tuples || returns != tc.returns {
				t.Fatalf("array/tuple projections and returns = %d/%d/%d, want %d/%d/%d", arrays, tuples, returns, tc.arrays, tc.tuples, tc.returns)
			}
		})
	}
}

func TestBuildShadowedBindings(t *testing.T) {
	decl, info := checkedFunc(t, `function pilot(items: string[], other: string[], choose: boolean): string {
  if (choose) { var items = other; var local = items[0]; }
  return items[0];
}`)
	f, err := BuildFunc(decl, info)
	if err != nil {
		t.Fatal(err)
	}
	var containers []ssa.Value
	for _, block := range f.graph.Blocks {
		for _, op := range block.Ops {
			if op.Kind == ssa.OpArrayGet {
				containers = append(containers, op.Args[0])
			}
		}
	}
	if len(containers) != 2 || containers[0] != f.graph.Params[1] || containers[1] != f.graph.Params[0] {
		t.Fatalf("shadowing conflates container identities: %v", containers)
	}
	count := 0
	for _, binding := range f.bindings {
		if binding.name == "items" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected distinct items bindings, got %d", count)
	}
}

func TestBuildKeepsCompleteTypes(t *testing.T) {
	decl, info := checkedFunc(t, `function pilot(own items: (string[], u64)[]): (string[], u64)[] { return items; }`)
	f, err := BuildFunc(decl, info)
	if err != nil {
		t.Fatal(err)
	}
	want := ast.ArrayType{Elem: ast.TupleType{Elems: []ast.Type{ast.ArrayType{Elem: ast.StringType{}}, ast.NumberType{Width: 64}}}}
	if !ast.Equal(f.result, want) || !ast.Equal(f.values[f.graph.Params[0].ID].typ, want) || f.modes[0] != ParamCounted {
		t.Fatal("lost nested semantic type or counted entry contract")
	}
}

func TestBuildUnsupportedIsExplicit(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"call-outside-program", `function pilot(items: string[]): string[] { return pass(items); } function pass(items: string[]): string[] { return items; }`, "callee is outside the typed program"},
		{"division", `function pilot(n: i32): i32 { return n / 2i32; }`, "unsupported scalar binary contract"},
		{"wide-arithmetic", `function pilot(n: i64): i64 { return n + 1i64; }`, "scalar binary requires"},
		{"defer", `function pilot(): string { defer cleanup(); return "a"; } function cleanup(): void {}`, "unsupported statement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decl, info := checkedFunc(t, tc.source)
			if f, err := BuildFunc(decl, info); f != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unsupported source silently accepted: f=%v err=%v", f, err)
			}
		})
	}
}
