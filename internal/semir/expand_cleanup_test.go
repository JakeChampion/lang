package semir

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func pendingCleanupFunc(t *testing.T) *Func {
	t.Helper()
	return pendingCleanupSource(t, `function pilot(flag: boolean): string {
  var items = ["saved"];
  defer { if (flag) { items = ["yes"] } else { items = ["no"] } }
  defer items = ["first"];
  return items[0];
}`)
}

func pendingCleanupSource(t *testing.T, source string) *Func {
	t.Helper()
	f := unverifiedCleanupSource(t, source)
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	return f
}

func unverifiedCleanupSource(t *testing.T, source string) *Func {
	t.Helper()
	prog, info := checkedProgram(t, source)
	decl := prog.Funcs[0]
	f := newFunc(decl.Name, decl.ReturnType)
	f.graph.NewBlock()
	for _, param := range decl.Params {
		f.addParam(param.Type, ParamValue, param.NamePos)
	}
	if err := buildBody(f, decl, info); err != nil {
		t.Fatal(err)
	}
	return f // Neither the source program nor checker information is retained.
}

func TestCleanupAdmissionUsesCompleteTypedCFG(t *testing.T) {
	for _, source := range []string{
		`function pilot(flag: boolean): string {
  if (flag) { var items = ["local"]; defer items = ["changed"]; }
  return "done";
}`,
		`function pilot(flag: boolean): string {
  loop {
    if (flag) { var items = ["local"]; defer items = ["changed"]; }
    break;
  }
  return "done";
}`,
	} {
		f := unverifiedCleanupSource(t, source)
		if err := ssa.Verify(f.graph); err != nil {
			t.Fatal(err)
		}
		if len(f.cleanups) != 1 || len(f.cleanups[0].replays) != 1 {
			t.Fatal("source producer must finish the graph before registration admission")
		}
		pos := f.cleanups[0].pos
		for _, phase := range []func(*Func) error{expandCleanups, Verify, promoteBindings, finishFlow} {
			if err := phase(f); err != nil {
				t.Fatal(err)
			}
		}
		f.cleanups[0].guarded.activation = 0
		want := fmt.Sprintf("at %d:%d: guarded replay", pos.Line, pos.Col)
		if err := Verify(f); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("complete-CFG admission lost its source diagnostic: %v", err)
		}
	}
}

func TestCleanupExpansionConsumesTypedSites(t *testing.T) {
	f := pendingCleanupFunc(t)
	if !f.unexpandedCleanups || len(f.cleanups) != 2 {
		t.Fatal("source construction must leave typed action invocations")
	}
	wantBlocks := len(f.graph.Blocks)
	for _, r := range f.cleanups {
		wantBlocks += len(r.replays) * (len(r.body.graph.Blocks) - 1)
		for _, site := range r.replays {
			if len(site.Ops) != 0 || (site.Term.Kind != ssa.TermBr && site.Term.Kind != ssa.TermRet) {
				t.Fatal("source construction already expanded an action")
			}
		}
	}
	if err := expandCleanups(f); err != nil {
		t.Fatal(err)
	}
	if f.unexpandedCleanups || !f.unpromotedBindings {
		t.Fatal("expansion must preserve binding places until their own pass")
	}
	if len(f.graph.Blocks) != wantBlocks {
		t.Fatal("expansion introduced unnecessary continuation blocks")
	}
	for _, r := range f.cleanups {
		for _, site := range r.replays {
			if len(site.Ops) < len(r.captures) {
				t.Fatal("missing late capture reads")
			}
			for i, id := range r.captures {
				op := site.Ops[i]
				if op.Kind != ssa.OpBindingRead || op.Imm != int64(id) || f.values[op.Result.ID].pos != r.pos {
					t.Fatal("capture read lost binding identity or action source position")
				}
			}
		}
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	if _, err := ownershipEffects(f); err != nil {
		t.Fatal(err)
	}
	if err := expandCleanups(f); err == nil {
		t.Fatal("must not replay expansion twice")
	}
}

func TestCleanupExpansionRejectsInvalidSitesBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Func)
		want string
	}{
		{"nonempty-site", func(f *Func) {
			f.addOp(f.cleanups[0].replays[0], ssa.OpConstInt, ast.NumberType{}, ast.Position{})
		}, "only its continuation"},
		{"duplicate-site", func(f *Func) {
			r := f.cleanups[0]
			r.replays = append(r.replays, r.replays[0])
		}, "invalid identity"},
		{"absent-capture", func(f *Func) {
			var id BindingID
			for i, binding := range f.bindings {
				if binding.name == "items" {
					id = BindingID(i + 1)
				}
			}
			for _, block := range f.graph.Blocks {
				block.Ops = slices.DeleteFunc(block.Ops, func(op *ssa.Op) bool {
					if op.Kind == ssa.OpBindingInit && op.Imm == int64(id) {
						delete(f.effectPositions, op)
						return true
					}
					return false
				})
			}
			// Remove ordinary source reads so only the semantic invocation can
			// expose absence. Keep its result definitions as correctly typed
			// empty arrays, preserving valid SSA and independent type verification.
			for _, block := range f.graph.Blocks {
				for _, op := range block.Ops {
					if op.Kind == ssa.OpBindingRead && op.Imm == int64(id) {
						op.Kind, op.Imm = ssa.OpArrayMake, 0
					}
				}
			}
		}, "captured binding 2 requires initialization"},
		{"pending-private-action", func(f *Func) {
			f.cleanups[0].body.unexpandedCleanups = true
		}, "invalid action"},
		{"unpromoted-private-action", func(f *Func) {
			f.cleanups[0].body.unpromotedBindings = true
		}, "invalid action"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := pendingCleanupFunc(t)
			tc.edit(f)
			if err := ssa.Verify(f.graph); err != nil {
				t.Fatal(err)
			}
			before := f.graph.String()
			if err := expandCleanups(f); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
			if f.graph.String() != before || !f.unexpandedCleanups {
				t.Fatal("invalid input was mutated or advanced to the expanded phase")
			}
		})
	}
}

func TestCleanupExpansionPreservesContinuationPhiOrder(t *testing.T) {
	f := pendingCleanupSource(t, `function pilot(flag: boolean): string {
  var i = 0i32;
  while (i < 2i32) {
    defer { if (flag) { i = i + 1i32 } else { i = i + 2i32 } }
  }
  return "done";
}`)
	r := f.cleanups[0]
	header := r.boundary.header
	if len(header.Preds) != 2 || len(r.body.graph.Blocks) < 2 {
		t.Fatal("fixture needs a joined loop header and multi-block action")
	}
	// Make operand order differ from CFG/source creation order. Distinct values
	// keep accidental phi reordering observable even though this phi is unused.
	slices.Reverse(header.Preds)
	var args []ssa.Value
	for i := range header.Preds {
		value := f.addOp(f.graph.Entry, ssa.OpConstInt, ast.NumberType{}, ast.Position{})
		f.graph.Entry.Ops[len(f.graph.Entry.Ops)-1].Imm = int64(i + 1)
		args = append(args, value)
	}
	value := f.addPhi(header, ast.NumberType{}, ast.Position{}, args...)
	var phi *ssa.Op
	for _, op := range header.Ops {
		if op.Result == value {
			phi = op
		}
	}
	oldPreds := append([]*ssa.Block(nil), header.Preds...)
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	if err := expandCleanups(f); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(phi.Args, args) {
		t.Fatal("continuation phi operands changed")
	}
	for i, pred := range oldPreds {
		if pred == r.replays[0] {
			if header.Preds[i] == pred || f.cleanupExits[0].finish != header.Preds[i] {
				t.Fatal("action exit and continuation predecessor must relocate together")
			}
		} else if header.Preds[i] != pred {
			t.Fatal("unrelated predecessor changed")
		}
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
}

func TestPendingCleanupCannotEscapeToValueConsumers(t *testing.T) {
	f := pendingCleanupFunc(t)
	before := f.graph.String()
	if err := promoteBindings(f); err == nil || !strings.Contains(err.Error(), "expanded cleanup") {
		t.Fatalf("promotion admitted pending actions: %v", err)
	}
	if _, err := ownershipEffects(f); err == nil || !strings.Contains(err.Error(), "expanded cleanup") {
		t.Fatalf("ownership admitted pending actions: %v", err)
	}
	f.unpromotedBindings = false
	if err := Verify(f); err == nil || !strings.Contains(err.Error(), "pending cleanup") {
		t.Fatalf("verifier admitted pending actions after place removal: %v", err)
	}
	if f.graph.String() != before {
		t.Fatal("rejected consumer mutated the graph")
	}
}

func TestCleanupExpansionLeavesActionFreeGraphUnchanged(t *testing.T) {
	f := pendingCleanupSource(t, `function pilot(): i32 { return 7i32; }`)
	before := f.graph.String()
	if err := expandCleanups(f); err != nil {
		t.Fatal(err)
	}
	if f.graph.String() != before || f.unexpandedCleanups {
		t.Fatal("action-free phase transition changed the graph or remained pending")
	}
	if err := promoteBindings(f); err != nil {
		t.Fatal(err)
	}
	if err := Verify(f); err != nil {
		t.Fatal(err)
	}
}
