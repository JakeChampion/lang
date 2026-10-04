package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

func TestStdlibBytesIntrinsicRequiresCanonicalDeclaration(t *testing.T) {
	canonical := ast.FuncDecl{
		Name: "__method_string_bytes", SourceFile: "stdlib://std/string.fern",
		Params:     []ast.Param{{Name: "s", Type: ast.StringType{}}},
		ReturnType: ast.ArrayType{Elem: ast.NumberType{Width: 8}},
	}
	for _, tc := range []struct {
		name string
		edit func(*ast.FuncDecl)
		want bool
	}{
		{"canonical", func(*ast.FuncDecl) {}, true},
		{"user method", func(f *ast.FuncDecl) { f.SourceFile = "user.fern" }, false},
		{"different method", func(f *ast.FuncDecl) { f.Name = "__method_string_custom" }, false},
		{"owned input", func(f *ast.FuncDecl) { f.Params[0].Own = true }, false},
		{"wrong receiver", func(f *ast.FuncDecl) { f.Params[0].Type = ast.BoolType{} }, false},
		{"extra parameter", func(f *ast.FuncDecl) { f.Params = append(f.Params, f.Params[0]) }, false},
		{"signed result", func(f *ast.FuncDecl) { f.ReturnType = ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: true}} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := canonical
			fn.Params = append([]ast.Param(nil), canonical.Params...)
			tc.edit(&fn)
			if got := stdlibBytesIntrinsic(&fn); got != tc.want {
				t.Fatalf("intrinsic = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestStdlibBytesIntrinsicBorrowsSourceAndReturnsFreshArray(t *testing.T) {
	prog, err := parser.Parse(`function __method_string_bytes(s: string): u8[] {
    let out: u8[] = __alloc_u8(s.len());
    let data: usize = __str_bytes(s, 0 as usize);
    __memcpy(out as usize, data, s.len());
    return out;
}
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	fn := prog.Funcs[0]
	// The same spelling in user code must not acquire the intrinsic contract.
	if got := inferParamCountedRetain(prog, info)[fn.Name]; len(got) != 1 || got[0] {
		t.Fatalf("user method received intrinsic credit: %v", got)
	}
	fn.SourceFile = "stdlib://std/string.fern"
	for name, got := range map[string][]bool{
		"counted":            inferParamCountedRetain(prog, info)[fn.Name],
		"no uncounted alias": inferParamNoUncountedAlias(prog, info)[fn.Name],
	} {
		if len(got) != 1 || !got[0] {
			t.Errorf("%s = %v, want [true]", name, got)
		}
	}
	if !findReturnsFreshBox(prog, info, nil, nil)[fn.Name] {
		t.Fatal("byte copy result is not owned")
	}
	out := lowerStdlibBytes(fn)
	if len(out.Ops) != 3 || out.Ops[1].Str != "__fern_string_bytes_copy" || out.ParamConsumed[0] {
		t.Fatalf("incorrect intrinsic lowering: %+v", out)
	}
	if _, ok := out.Ops[1].Ext.ArgTypes[0].(ast.StringType); !ok {
		t.Fatal("runtime call lost its string argument type")
	}
}

func TestRawStringPointerEscapeRemainsUncredited(t *testing.T) {
	got := paramCountedFor(t, `function pointer(s: string): usize {
    return __str_bytes(s, 0 as usize);
}
function main(): i32 { return 0; }`, "pointer")
	if len(got) != 1 || got[0] {
		t.Fatalf("raw pointer escape credited: %v", got)
	}
}
