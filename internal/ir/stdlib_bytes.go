package ir

import "github.com/jakechampion/lang/internal/ast"

// The primary compiler already lowers std/string.bytes to its typed str_bytes
// operation. Keep the bootstrap's input loan and fresh result explicit too.
// The source check preserves user-defined methods with the same spelling.
func stdlibBytesIntrinsic(fn *ast.FuncDecl) bool {
	if fn.Name != "__method_string_bytes" || fn.SourceFile != "stdlib://std/string.fern" || len(fn.Params) != 1 {
		return false
	}
	_, str := fn.Params[0].Type.(ast.StringType)
	arr, ok := fn.ReturnType.(ast.ArrayType)
	if !str || !ok || fn.Params[0].Own {
		return false
	}
	byteType, ok := arr.Elem.(ast.NumberType)
	return ok && byteType.NormalWidth() == 8 && !byteType.Signed
}

func lowerStdlibBytes(fn *ast.FuncDecl) *Func {
	return &Func{
		Name: fn.Name, Params: fn.Params, ReturnType: fn.ReturnType,
		ParamConsumed: []bool{false},
		Ops: []Op{
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpCallDirect, Runtime: true, Str: "__fern_string_bytes_copy", Width: ResAddr, I32: 1,
				Ext: &OpExt{ArgTypes: []ast.Type{ast.StringType{}}}},
			{Kind: OpReturn},
		},
	}
}
