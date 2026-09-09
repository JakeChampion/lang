package sourcelint

import (
	"os"
	"slices"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// The semantic type model must be usable by both checked syntax and pre-RC IR.
// Importing the checker here would create a cycle as soon as AST carries Type.
func TestSelfHostSemanticTypeBoundary(t *testing.T) {
	src, err := os.ReadFile("../../examples/self_host/typeinfo.fern")
	if err != nil {
		t.Fatal(err)
	}
	p, err := parser.Parse(string(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Imports) != 0 || len(p.PubUses) != 0 {
		t.Fatal("semantic types must not depend on AST, checker, parser or physical lowering")
	}
	// Preserve the existing discriminants and complete recursive field types.
	// An intentional model extension must update this contract and its consumers.
	wantNames := []string{"TypeI32", "TypeBool", "TypeString", "TypeFloat", "TypeArray", "TypeStruct", "TypeTuple", "TypeFunc", "TypeMap", "TypeUnion", "TypeDyn", "TypeVoid", "TypeUnknown"}
	wantFields := [][]string{
		{"is_char:boolean", "width:i32", "unsigned:boolean"},
		{"tag:i32"}, {"tag:i32"}, {"tag:i32"}, {"elem:Type"},
		{"name:string", "args:Type[]"}, {"elements:Type[]"},
		{"param_types:Type[]", "ret_type:Type", "params_known:boolean"},
		{"key:Type", "value:Type"}, {"name:string", "args:Type[]"},
		{"traits:string"}, {"tag:i32"}, {"reason:string"},
	}
	if len(p.Structs) != len(wantNames) || len(p.Unions) != 1 || p.Unions[0].Name != "Type" {
		t.Fatal("semantic type declarations changed; audit all semantic consumers")
	}
	var members []string
	for _, m := range p.Unions[0].Members {
		members = append(members, m.String())
	}
	if !slices.Equal(members, wantNames) {
		t.Fatalf("semantic union order = %v, want %v", members, wantNames)
	}
	for i, d := range p.Structs {
		var fields []string
		for _, f := range d.Fields {
			fields = append(fields, f.Name+":"+f.Type.String())
		}
		if d.Name != wantNames[i] || !d.Public || !slices.Equal(fields, wantFields[i]) {
			t.Errorf("semantic declaration %s fields = %v, want public %s %v", d.Name, fields, wantNames[i], wantFields[i])
		}
	}
	checker := parseSelfHostChecker(t)
	for _, d := range checker.Structs {
		if slices.Contains(wantNames, d.Name) {
			t.Errorf("checker duplicates shared semantic type %s", d.Name)
		}
	}
	for _, d := range checker.Unions {
		if d.Name == "Type" {
			t.Error("checker duplicates the shared Type union")
		}
	}
	// A real production record must carry the shared type, not a copied model.
	found := false
	for _, d := range checker.Structs {
		if d.Name != "CheckResult" {
			continue
		}
		for _, f := range d.Fields {
			if f.Name == "ty" {
				found = ast.Equal(f.Type, ast.StructType{Name: "typeinfo.Type"})
			}
		}
	}
	if !found {
		t.Fatal("CheckResult must use the shared typeinfo.Type")
	}
}
