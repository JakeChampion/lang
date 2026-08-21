package arm64

import (
	"strings"
	"testing"
)

// IR-level dead-function elimination (#4377), the x86-64 backend's twin — see
// its dead_funcs_test.go for what each case is guarding.

func TestIRDeadFunctionCullDropsOrphanedCallee(t *testing.T) {
	asm := compile(t, `function only_from_dead_branch(n: i32): i32 { return n * 3; }
function main(): i32 {
	if (false) { return only_from_dead_branch(4); }
	return 0;
}`, Options{})
	if strings.Contains(asm, AsmFnName("only_from_dead_branch")) {
		t.Errorf("callee reachable only from a folded-away branch was still emitted:\n%s", asm)
	}
	if !strings.Contains(asm, AsmFnName("main")+":") {
		t.Error("the cull removed main")
	}
}

func TestIRDeadFunctionCullKeepsVtableTargets(t *testing.T) {
	asm := compile(t, `trait Error { function message(self: Self): string; }
struct NotFound { what: string }
impl Error for NotFound { function message(self: Self): string { return self.what; } }
function main(): i32 {
	var e: dyn Error = NotFound { what: "ab" } as dyn Error;
	return e.message().len();
}`, Options{})
	if !strings.Contains(asm, "__vtable_Error_NotFound:") {
		t.Fatal("vtable cell not emitted; test can't guard what it points at")
	}
	for _, fn := range []string{"__method_NotFound_message", "__drop_struct_NotFound", "__drop_dyn_Error"} {
		if !strings.Contains(asm, AsmFnName(fn)+":") {
			t.Errorf("vtable-reachable %q was culled, leaving a dangling label", AsmFnName(fn))
		}
	}
}
