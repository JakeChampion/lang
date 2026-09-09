package ir

import (
	"reflect"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

func TestFoldNullIdentityCalls(t *testing.T) {
	zero := Op{Kind: OpConstI32}
	call := Op{Kind: OpCallDirect, Str: "cleanup", I32: 1}
	cases := []struct {
		name       string
		ops, want  []Op
		identities map[string]bool
	}{
		{"certified", []Op{zero, call, {Kind: OpReturn}}, []Op{zero, {Kind: OpReturn}}, map[string]bool{"cleanup": true}},
		{"unknown", []Op{zero, call}, []Op{zero, call}, nil},
		{"non-null", []Op{{Kind: OpConstI32, I32: 1}, call}, []Op{{Kind: OpConstI32, I32: 1}, call}, map[string]bool{"cleanup": true}},
		{"runtime-namespace", []Op{zero, {Kind: OpCallDirect, Str: "cleanup", I32: 1, Runtime: true}}, []Op{zero, {Kind: OpCallDirect, Str: "cleanup", I32: 1, Runtime: true}}, map[string]bool{"cleanup": true}},
		{"wrong-arity", []Op{zero, {Kind: OpCallDirect, Str: "cleanup", I32: 2}}, []Op{zero, {Kind: OpCallDirect, Str: "cleanup", I32: 2}}, map[string]bool{"cleanup": true}},
		{"indirect", []Op{zero, {Kind: OpCallIndirect, Str: "cleanup", I32: 1}}, []Op{zero, {Kind: OpCallIndirect, Str: "cleanup", I32: 1}}, map[string]bool{"cleanup": true}},
		{"several", []Op{zero, call, {Kind: OpDrop}, zero, call, {Kind: OpReturn}}, []Op{zero, {Kind: OpDrop}, zero, {Kind: OpReturn}}, map[string]bool{"cleanup": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]Op(nil), tc.ops...)
			if got := foldNullIdentityCalls(tc.ops, tc.identities); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if !reflect.DeepEqual(before, tc.ops) {
				t.Fatal("mutated input")
			}
		})
	}
}

func TestGeneratedNullIdentityContracts(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, `struct Box { text: string }
trait Drop { function drop(self: Self): void; }
impl Drop for Box { function drop(self: Self): void { print("user drop"); } }
function f(items: (Box, string)[]): i32 { return items.len(); }
function main(): i32 { return f([(Box { text: "hello" }, "world")]); }`, ptrW)
		contracts := 0
		for _, fn := range p.Funcs {
			if !fn.NullIdentity {
				continue
			}
			contracts++
			if len(fn.Params) != 1 {
				t.Fatalf("%s: null contract requires exactly one parameter", fn.Name)
			}
			// Verify the generated body independently of call-site elimination.
			probe := *fn
			probe.NullIdentity = false
			probe.Params = nil
			probe.Locals = []*ast.Var{{Name: "arg", Type: fn.Params[0].Type}}
			probe.Ops = append([]Op{{Kind: OpConstI32}, {Kind: OpStoreLocal, I32: 0}}, fn.Ops...)
			OptimizeCleanup(&Program{Funcs: []*Func{&probe}, PtrW: ptrW})
			for _, op := range probe.Ops {
				switch op.Kind {
				case OpConstI32, OpStoreLocal, OpLoadLocal, OpReturn:
				default:
					t.Fatalf("ptrW=%d %s: observable or unproven null behavior: %v", ptrW, fn.Name, probe.Ops)
				}
			}
			if len(probe.Ops) < 2 || probe.Ops[len(probe.Ops)-2].Kind != OpConstI32 || probe.Ops[len(probe.Ops)-2].I32 != 0 || probe.Ops[len(probe.Ops)-1].Kind != OpReturn {
				t.Fatalf("ptrW=%d %s: did not return constant zero: %v", ptrW, fn.Name, probe.Ops)
			}
		}
		if contracts < 2 {
			t.Fatalf("ptrW=%d: only %d generated contracts, want struct and tuple", ptrW, contracts)
		}
	}
}

func TestOptimizeCleanupNullIdentityCalls(t *testing.T) {
	cleanup := &Func{Name: "cleanup", NullIdentity: true, Params: []ast.Param{{Name: "p", Type: ast.NumberType{}}}, ReturnType: ast.NumberType{}, Ops: []Op{{Kind: OpLoadLocal}, {Kind: OpReturn}}}
	caller := &Func{Name: "caller", Locals: []*ast.Var{{Name: "slot", Type: ast.NumberType{}}}, Ops: []Op{
		{Kind: OpConstI32}, {Kind: OpStoreLocal}, {Kind: OpLoadLocal},
		{Kind: OpCallDirect, Str: "cleanup", I32: 1}, {Kind: OpDrop},
		{Kind: OpConstI32, I32: 4096}, {Kind: OpCallDirect, Str: "cleanup", I32: 1}, {Kind: OpDrop},
		{Kind: OpConstI32}, {Kind: OpCallDirect, Str: "__drop_struct_user", I32: 1}, {Kind: OpReturn},
	}}
	untrusted := &Func{Name: "__drop_struct_user", Params: cleanup.Params, ReturnType: ast.NumberType{}, Ops: []Op{
		{Kind: OpLoadLocal}, {Kind: OpCallDirect, Str: "observable", I32: 1}, {Kind: OpReturn},
	}}
	p := &Program{Funcs: []*Func{caller, cleanup, untrusted}, PtrW: 8}
	OptimizeCleanup(p)
	var calls []string
	for _, op := range caller.Ops {
		if op.Kind == OpCallDirect {
			calls = append(calls, op.Str)
		}
	}
	if !reflect.DeepEqual(calls, []string{"cleanup", "__drop_struct_user"}) {
		t.Fatalf("lost a live or uncertified call, or kept the null call: %v", caller.Ops)
	}
	before := p.String()
	OptimizeCleanup(p)
	if p.String() != before {
		t.Fatal("cleanup did not reach its fixed point")
	}
}
