package semir

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func iterationBoundaryProgram(t *testing.T) *Program {
	t.Helper()
	prog, info := checkedProgram(t, `function pilot(): void {
  defer sink();
  outer: loop { defer sink(); loop { defer sink(); break outer; } }
}
function sink(): void {}`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVerifyCleanupRejectsMalformedBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Func)
		want string
	}{
		{"missing-root", func(f *Func) { f.boundaries = nil }, "missing function boundary"},
		{"foreign-owner", func(f *Func) { f.boundaries[1].owner = &Func{} }, "invalid boundary identity"},
		{"duplicate-identity", func(f *Func) { f.boundaries = append(f.boundaries, f.boundaries[1]) }, "invalid boundary identity"},
		{"duplicate-entry", func(f *Func) { f.boundaries[2].entry = f.boundaries[1].entry }, "invalid boundary identity"},
		{"root-parent", func(f *Func) { f.boundaries[0].parent = f.boundaries[1] }, "invalid function boundary"},
		{"parent-cycle", func(f *Func) { f.boundaries[1].parent = f.boundaries[2] }, "invalid iteration boundary"},
		{"foreign-header", func(f *Func) { f.boundaries[1].header = &ssa.Block{} }, "invalid iteration boundary"},
		{"foreign-exit", func(f *Func) { f.boundaries[1].exit = &ssa.Block{} }, "invalid iteration boundary"},
		{"missing-end", func(f *Func) { f.cleanupExits[0].ends = f.cleanupExits[0].ends[:1] }, "omits a crossed boundary"},
		{"reversed-ends", func(f *Func) {
			e := f.cleanupExits[0]
			e.ends[0], e.ends[1] = e.ends[1], e.ends[0]
		}, "invalid crossed boundary sequence"},
		{"wrong-end-block", func(f *Func) { f.cleanupExits[0].ends[0].block = f.graph.Entry }, "invalid crossed boundary sequence"},
		{"excess-crossing", func(f *Func) {
			e := f.cleanupExits[0]
			e.ends = append(e.ends, cleanupEnd{f.boundaries[0], e.finish})
		}, "crosses beyond its declared boundary"},
		{"break-as-continue", func(f *Func) { f.cleanupExits[0].kind = cleanupContinue }, "continue targets a different iteration"},
		{"invalid-kind", func(f *Func) { f.cleanupExits[0].kind = cleanupExitKind(255) }, "invalid cleanup exit kind"},
		{"wrong-target", func(f *Func) { f.cleanupExits[0].target = f.boundaries[1].header }, "declared branch"},
		{"missing-return-exit", func(f *Func) { f.cleanupExits = f.cleanupExits[:1] }, "return lacks a function cleanup exit"},
		{"wrong-action-boundary", func(f *Func) { f.cleanups[2].boundary = f.boundaries[1] }, "registration belongs to a different active boundary"},
		{"wrong-parent-chain", func(f *Func) { f.boundaries[2].parent = f.boundaries[0] }, "invalid crossed boundary sequence"},
		{"missing-replay", func(f *Func) { f.cleanups[2].replays = nil }, "boundary closes with pending actions"},
		{"missing-iteration-exit", func(f *Func) { f.cleanupExits = f.cleanupExits[1:] }, "most recent pending registration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := iterationBoundaryProgram(t)
			f := p.funcs[0]
			if len(f.boundaries) != 3 || len(f.cleanupExits) != 2 || len(f.cleanups) != 3 {
				t.Fatal("fixture lost its nested cleanup boundaries")
			}
			tc.edit(f)
			if err := ssa.Verify(f.graph); err != nil {
				t.Fatalf("mutation must preserve valid SSA: %v", err)
			}
			if err := VerifyProgram(p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestVerifyCleanupRejectsWrongActiveParent(t *testing.T) {
	prog, info := checkedProgram(t, `function pilot(): void { loop { loop { return; } } }`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	f := p.funcs[0]
	// Keep the declared ancestor sequence internally consistent, but make it
	// disagree with the actual CFG nesting. Only the independent flow proof
	// should reject this mutation, even when there are no cleanup actions.
	f.boundaries[2].parent = f.boundaries[0]
	e := f.cleanupExits[0]
	e.ends = []cleanupEnd{e.ends[0], e.ends[2]}
	if _, err := verifyCleanupBoundaries(f, ssa.BuildDomTree(f.graph)); err != nil {
		t.Fatalf("mutation must preserve structural boundary validity: %v", err)
	}
	if err := VerifyProgram(p); err == nil || !strings.Contains(err.Error(), "boundary entry has the wrong active parent") {
		t.Fatalf("wrong active parent accepted: %v", err)
	}
}

func TestVerifyCleanupRejectsUnclosedIterationCycle(t *testing.T) {
	prog, info := checkedProgram(t, `function pilot(): void { loop { defer sink(); } }
function sink(): void {}`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	f := p.funcs[0]
	// Registration and replay remain balanced, but their history must not reset
	// without a verified close. There is no return whose metadata could reject
	// the mutation before the independent backedge proof runs.
	f.cleanupExits = nil
	if err := ssa.Verify(f.graph); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyCleanupBoundaries(f, ssa.BuildDomTree(f.graph)); err != nil {
		t.Fatalf("mutation must reach the flow proof: %v", err)
	}
	if err := VerifyProgram(p); err == nil || !strings.Contains(err.Error(), "registration state disagrees across a join or cycle") {
		t.Fatalf("unclosed iteration cycle accepted: %v", err)
	}
}

func TestVerifyCleanupBoundaryDiagnosticSource(t *testing.T) {
	for _, iteration := range []bool{false, true} {
		p := iterationBoundaryProgram(t)
		f := p.funcs[0]
		scope := f.boundaries[0]
		if iteration {
			exit := f.cleanupExits[0]
			scope = exit.from
			exit.kind = cleanupContinue
		} else {
			scope.header = scope.entry
		}
		if scope.pos.Line <= 0 || scope.pos.Col <= 0 {
			t.Fatal("source boundary lost its origin")
		}
		want := fmt.Sprintf("cleanup at %d:%d:", scope.pos.Line, scope.pos.Col)
		if err := VerifyProgram(p); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want source-anchored diagnostic %q, got %v", want, err)
		}
	}
}
