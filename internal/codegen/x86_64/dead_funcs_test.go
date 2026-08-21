package x86_64

import (
	"strings"
	"testing"
)

// IR-level dead-function elimination (#4377). The AST tree-shaker keeps a
// function any source-level call site names; these pin what the IR pass adds on
// top of that — and, more importantly, what it must NOT take.

// A callee whose only call site sits in a branch FlattenBranches proves dead is
// unreachable by the time the cull runs, even though the tree-shaker saw a
// reference to it.
func TestIRDeadFunctionCullDropsOrphanedCallee(t *testing.T) {
	asm := compile(t, `function only_from_dead_branch(n: i32): i32 { return n * 3; }
function main(): i32 {
	if (false) { return only_from_dead_branch(4); }
	return 0;
}`)
	if strings.Contains(asm, AsmFnName("only_from_dead_branch")) {
		t.Errorf("callee reachable only from a folded-away branch was still emitted:\n%s", asm)
	}
	if !strings.Contains(asm, AsmFnName("main")+":") {
		t.Error("the cull removed main")
	}
}

// A `dyn Trait` impl method and the concrete type's drop are named only by the
// static vtable cell, which the reachability walk cannot follow. Culling either
// leaves the cell pointing at a symbol the assembler never defines, so both are
// rooted explicitly.
func TestIRDeadFunctionCullKeepsVtableTargets(t *testing.T) {
	asm := compile(t, `trait Error { function message(self: Self): string; }
struct NotFound { what: string }
impl Error for NotFound { function message(self: Self): string { return self.what; } }
function main(): i32 {
	var e: dyn Error = NotFound { what: "ab" } as dyn Error;
	return e.message().len();
}`)
	if !strings.Contains(asm, "__vtable_Error_NotFound:") {
		t.Fatal("vtable cell not emitted; test can't guard what it points at")
	}
	for _, fn := range []string{"__method_NotFound_message", "__drop_struct_NotFound", "__drop_dyn_Error"} {
		if !strings.Contains(asm, AsmFnName(fn)+":") {
			t.Errorf("vtable-reachable %q was culled, leaving a dangling label", AsmFnName(fn))
		}
	}
}
