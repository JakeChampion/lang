package semir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

func cleanupFlowProgram(t *testing.T) *Program {
	t.Helper()
	prog, info := checkedProgram(t, `function pilot(flag: boolean): void {
  if (flag) { sink(); }
  defer sink();
}
function sink(): void {}`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVerifyCleanupRejectsDynamicReplayCycles(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Func, *cleanupRegion)
	}{
		{"replay-self-loop", func(f *Func, r *cleanupRegion) {
			exit := f.graph.NewBlock()
			f.graph.SetRet(exit, ssa.Value{})
			f.graph.SetBrIf(r.replays[0], f.graph.Params[0], r.replays[0], exit)
		}},
		{"replay-cycle-no-return", func(f *Func, r *cleanupRegion) {
			f.graph.SetBr(r.replays[0], r.replays[0])
		}},
		{"balanced-reregistration-cycle", func(f *Func, r *cleanupRegion) {
			exit := f.graph.NewBlock()
			f.graph.SetRet(exit, ssa.Value{})
			f.graph.SetBrIf(r.replays[0], f.graph.Params[0], r.register, exit)
		}},
		{"indirect-replay-cycle", func(f *Func, r *cleanupRegion) {
			back, exit := f.graph.NewBlock(), f.graph.NewBlock()
			f.graph.SetRet(exit, ssa.Value{})
			f.graph.SetBr(back, r.replays[0])
			f.graph.SetBrIf(r.replays[0], f.graph.Params[0], back, exit)
		}},
		{"incompatible-pending-join", func(f *Func, r *cleanupRegion) {
			join := f.graph.NewBlock()
			f.graph.SetBr(join, join)
			f.graph.SetBr(r.replays[0], join)
			f.graph.SetBrIf(r.register, f.graph.Params[0], r.replays[0], join)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := cleanupFlowProgram(t)
			f := p.funcs[0]
			tc.edit(f, f.cleanups[0])
			if err := ssa.Verify(f.graph); err != nil {
				t.Fatalf("test must preserve valid SSA: %v", err)
			}
			if err := VerifyProgram(p); err == nil || !strings.Contains(err.Error(), "cleanup") {
				t.Fatalf("dynamic cleanup cycle accepted or failed outside cleanup proof: %v", err)
			}
		})
	}
}

func TestVerifyCleanupAllowsPendingActionOnNonreturningPath(t *testing.T) {
	prog, info := checkedProgram(t, `function pilot(flag: boolean): void {
  defer sink();
  if (flag) { loop {} }
}
function sink(): void {}`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyProgram(p); err != nil {
		t.Fatal(err)
	}
}
