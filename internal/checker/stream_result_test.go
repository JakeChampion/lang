package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// TestStreamResultColorlessTransform covers the colorless `stream[T]` result
// transform (docs/STREAM-TYPE-SURFACE.md): an `@import async function body():
// stream[u8]` is delivered incrementally over the wire but yields the fully
// collected `u8[]` at the call site. The checker rewrites the effective return
// type to `u8[]` (so `var b: u8[] = body()` type-checks with no special call-site
// rule) and stashes the element type on the FuncDecl for codegen.
func TestStreamResultColorlessTransform(t *testing.T) {
	src := `@import("test:dep/d", "body") async function body(): stream[u8];
async function run(): i32 { var b: u8[] = body(); return b.len(); }
`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check (stream result should collect to u8[]): %v", err)
	}
	sig, ok := info.FuncSigs["body"]
	if !ok {
		t.Fatalf("no FuncSig for body")
	}
	at, ok := sig.Result.(ast.ArrayType)
	if !ok {
		t.Fatalf("body's effective result should be rewritten to u8[]; got %T (%v)", sig.Result, sig.Result)
	}
	if n, ok := at.Elem.(ast.NumberType); !ok || n.NormalWidth() != 8 {
		t.Errorf("collected element type should be u8; got %v", at.Elem)
	}
	// The element type is stashed on the FuncDecl for the codegen stream-collect ABI.
	var found bool
	for _, fn := range prog.Funcs {
		if fn.Name == "body" {
			found = true
			if fn.StreamResultElem == nil {
				t.Errorf("body.StreamResultElem should be set to the stream element type")
			}
		}
	}
	if !found {
		t.Fatalf("body FuncDecl not found")
	}
}
