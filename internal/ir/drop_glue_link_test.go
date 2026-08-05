package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
)

// Every `__drop_*` call the lowering emits must have a generated body in the
// same program — the drop-glue worklist derives generation from emitted
// calls, so any call site that fires outside the worklist's gate becomes an
// undefined symbol at link time (native) or an unknown-callee build error
// (wasm).
//
// The regression this pins: emitArraySet's rc-tracked element path (the
// `.with` overwrite-drop, #4187) emitted `__drop_enum_<E>` through
// dropStructField UNGATED, while the worklist that generates those bodies
// runs only under RcFreeEnabled — so every free-OFF build of a `.with` on an
// rc-tracked-element array referenced a drop fn that was never generated.
// First hit by std/regex's `RInst[]` `.with` patching once regex_captures
// landed: `__drop_enum_regex__RInst` undefined in every free-off fixture
// link (the #4958 red x86_64/arm64/wasm lanes).
func TestDropCallsHaveGeneratedBodies(t *testing.T) {
	const src = `
enum Inst { IChar(i32), IClass(string), IMatch }

function patch(prog: Inst[], at: i32, v: Inst): Inst[] {
    return prog.with(at, v);
}
function main(): i32 {
    var prog: Inst[] = [IChar(97), IMatch];
    prog = patch(prog, 1, IClass("x"));
    return prog.len() - 2;
}`
	for _, free := range []bool{false, true} {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if err := constfold.Fold(prog, nil); err != nil {
			t.Fatalf("constfold: %v", err)
		}
		info, err := checker.Check(prog)
		if err != nil {
			t.Fatalf("check: %v", err)
		}
		prev := ast.RcFreeEnabled
		ast.RcFreeEnabled = free
		ip, lowerErr := ir.LowerWith(prog, info, 8)
		ast.RcFreeEnabled = prev
		if lowerErr != nil {
			t.Fatalf("lower (free=%v): %v", free, lowerErr)
		}
		defined := map[string]bool{}
		for _, fn := range ip.Funcs {
			defined[fn.Name] = true
		}
		for _, fn := range ip.Funcs {
			for _, op := range fn.Ops {
				if op.Kind != ir.OpCallDirect || !strings.HasPrefix(op.Str, "__drop_") {
					continue
				}
				if !defined[op.Str] {
					t.Errorf("free=%v: %s calls %s but no such function was generated (undefined symbol at link)", free, fn.Name, op.Str)
				}
			}
		}
	}
}
