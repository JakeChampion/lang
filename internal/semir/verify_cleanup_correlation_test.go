package semir

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func TestConditionalCleanupAdmission(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"branch-local", `if (flag) { var xs: i32[] = [1]; defer xs = [2]; }`},
		{"late-replacement", `var xs: i32[] = [1]; if (flag) { defer xs = [2]; } xs = [3];`},
		{"exclusive", `if (flag) { var xs: i32[] = [1]; defer xs = [2]; } else { var ys: i32[] = [3]; defer ys = [4]; }`},
		{"independent", `if (flag) { var xs: i32[] = [1]; defer xs = [2]; } if (other) { var ys: i32[] = [3]; defer ys = [4]; }`},
		{"mixed-order", `var xs: i32[] = [1]; defer xs = [2]; if (flag) { defer xs = [3]; } defer xs = [4];`},
		{"iteration-reset", `var i = 0i32; while (i < 3i32) { if (flag) { var xs: i32[] = [1]; defer xs = [2]; } i = i + 1i32; }`},
		{"nested-iteration", `var xs: i32[] = [1]; if (flag) { defer xs = [2]; } var i = 0i32; while (i < 3i32) { if (other) { var ys: i32[] = [3]; defer ys = [4]; } i = i + 1i32; }`},
		{"continue", `var i = 0i32; while (i < 3i32) { i = i + 1i32; if (flag) { var xs: i32[] = [1]; defer xs = [2]; } if (other) { continue; } }`},
		{"labelled-break", `outer: loop { if (flag) { var xs: i32[] = [1]; defer xs = [2]; } loop { if (other) { var ys: i32[] = [3]; defer ys = [4]; } break outer; } }`},
		{"return", `if (flag) { var xs: i32[] = [1]; defer xs = [2]; } if (other) { return; }`},
		{"nonreturning", `if (flag) { var xs: i32[] = [1]; defer xs = [2]; } if (other) { loop {} }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := unverifiedCleanupSource(t, `function pilot(flag: boolean, other: boolean): void {`+tc.body+`}`)
			if err := Verify(f); err != nil {
				t.Fatal(err)
			}
			for _, phase := range []struct {
				name string
				run  func(*Func) error
			}{{"expand", expandCleanups}, {"verify expanded", Verify}, {"promote", promoteBindings}, {"finish", finishFlow}} {
				if err := phase.run(f); err != nil {
					t.Fatalf("%s: %v", phase.name, err)
				}
			}
			if _, err := ownershipEffects(f); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func conditionalCleanupFixture(t *testing.T) *Func {
	t.Helper()
	return unverifiedCleanupSource(t, `function pilot(flag: boolean): void {
  if (flag) { var xs: i32[] = [1]; defer xs = [2]; defer xs = [3]; }
}`)
}

func TestConditionalCleanupRejectsInvalidCorrelations(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		edit       func(*Func)
	}{
		{"lifo", "most recent", func(f *Func) {
			f.cleanups[0].replays, f.cleanups[1].replays = f.cleanups[1].replays, f.cleanups[0].replays
		}},
		{"missing-replay", "pending action", func(f *Func) { f.cleanups[0].replays = nil }},
		{"duplicate-replay", "more than once", func(f *Func) {
			a := f.cleanups[0]
			old, repeat := a.replays[0], f.graph.NewBlock()
			f.graph.SetBr(old, repeat)
			f.graph.SetRet(repeat, ssa.Value{})
			a.replays = append(a.replays, repeat)
			f.cleanupExits[0].finish = repeat
			f.cleanupExits[0].ends[0].block = repeat
		}},
		{"absent-capture", "absent on a registered", func(f *Func) {
			id := f.cleanups[0].captures[0]
			for _, block := range f.graph.Blocks {
				block.Ops = slices.DeleteFunc(block.Ops, func(op *ssa.Op) bool {
					if op.Kind == ssa.OpBindingInit && op.Imm == int64(id) {
						delete(f.effectPositions, op)
						return true
					}
					return false
				})
			}
		}},
		{"opposite-branch-capture", "absent on a registered", func(f *Func) {
			registered := f.cleanups[0].register
			other := f.graph.Entry.Term.False
			if other == nil || other == registered {
				t.Fatal("fixture must have separate registration and unentered branches")
			}
			other.Ops = append(other.Ops, registered.Ops...)
			registered.Ops = nil
		}},
		{"wrong-active-boundary", "lifetime boundary", func(f *Func) {
			f.bindings[f.cleanups[0].captures[0]-1].boundary = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := conditionalCleanupFixture(t)
			tc.edit(f)
			if err := Verify(f); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestConditionalCleanupRejectsExpiredNestedCapture(t *testing.T) {
	f := unverifiedCleanupSource(t, `function pilot(flag: boolean): void {
  var xs: i32[] = [1]; if (flag) { defer xs = [2]; }
  loop { var ys: i32[] = [3]; defer ys = [4]; break; }
}`)
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	f.cleanups[0].captures[0] = f.cleanups[1].captures[0]
	if err := Verify(f); err == nil || !strings.Contains(err.Error(), "absent on a registered") {
		t.Fatalf("outer action recovered an expired inner binding: %v", err)
	}
}

func TestConditionalCleanupDoesNotRelaxOrdinaryReads(t *testing.T) {
	for _, kind := range []ssa.OpKind{ssa.OpBindingRead, ssa.OpBindingReplace} {
		f := conditionalCleanupFixture(t)
		id := f.cleanups[0].captures[0]
		join := f.cleanups[1].replays[0].Preds[0]
		if kind == ssa.OpBindingRead {
			f.readBinding(join, id, ast.Position{Line: 42, Col: 3})
		} else {
			value := f.addOp(join, ssa.OpArrayMake, f.bindings[id-1].typ, ast.Position{})
			f.writeBinding(join, kind, id, value, ast.Position{Line: 42, Col: 3})
		}
		if err := Verify(f); err == nil || !strings.Contains(err.Error(), "42:3: read or replacement requires initialization") {
			t.Fatalf("ordinary access lost must-initialization rule: %v", err)
		}
	}
}

func BenchmarkConditionalCleanupAdmission(b *testing.B) {
	for _, count := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("actions-%d", count), func(b *testing.B) {
			var source strings.Builder
			source.WriteString(`function pilot(flag: boolean): void {`)
			for i := 0; i < count; i++ {
				source.WriteString(`if (flag) { var xs: i32[] = [1]; defer xs = [2]; }`)
			}
			source.WriteString(`}`)
			prog, info := checkedProgram(b, source.String())
			f := newFunc("pilot", ast.VoidType{})
			f.graph.NewBlock()
			f.addParam(ast.BoolType{}, ParamValue, ast.Position{})
			if err := buildBody(f, prog.Funcs[0], info); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := Verify(f); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
