package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

// clayoutWidth is the size in bytes of a field type in a C struct, which is
// also its alignment, or 0 when the type has no C form.
// compiler/parser.fern's clayout_width holds the same table.
func clayoutWidth(t ast.Type) int {
	switch t.String() {
	case "u8":
		return 1
	case "i32", "u32", "f32":
		return 4
	case "i64", "u64", "usize", "f64":
		return 8
	}
	return 0
}

func clayoutRound(n, align int) int {
	return (n + align - 1) / align * align
}

// clayoutOffsets is the C offset of each field of sd, and then the size of
// the struct, which rounds up to the largest alignment of a field.
func clayoutOffsets(sd *ast.StructDecl) []int {
	out := make([]int, 0, len(sd.Fields)+1)
	off, align := 0, 1
	for _, f := range sd.Fields {
		w := clayoutWidth(f.Type)
		off = clayoutRound(off, w)
		out = append(out, off)
		off += w
		align = max(align, w)
	}
	return append(out, clayoutRound(off, align))
}

// clayoutGap is the refusal of @derive(CLayout) for sd, or "".
func clayoutGap(sd *ast.StructDecl) string {
	if len(sd.TypeParams) > 0 {
		return "a struct with a C layout has no type parameters"
	}
	for _, f := range sd.Fields {
		if clayoutWidth(f.Type) == 0 {
			return "field " + f.Name + " has type " + f.Type.String() + ", which has no C form; a field of a C layout is u8, i32, u32, i64, u64, usize, f32 or f64"
		}
	}
	return ""
}

func usizeLit(n int) ast.Expr {
	return &ast.NumberLit{Value: int64(n), Width: ast.WidthPtr, IsUnsigned: true}
}

func clayoutAddr(off int) ast.Expr {
	p := &ast.Ident{Name: "p"}
	if off == 0 {
		return p
	}
	return &ast.Binary{Op: "+", Left: p, Right: usizeLit(off)}
}

func clCall(name string, args ...ast.Expr) ast.Expr {
	return &ast.Call{Callee: &ast.Ident{Name: name}, Args: args}
}

func clCast(e ast.Expr, t ast.Type) ast.Expr {
	return &ast.CastExpr{Inner: e, Target: t}
}

var (
	clU8  = ast.NumberType{Width: 8, Signed: false, Spelling: "u8"}
	clI32 = ast.NumberType{Spelling: "i32"}
	clU32 = ast.NumberType{Width: 32, Signed: false, Spelling: "u32"}
	clI64 = ast.NumberType{Width: 64, Signed: true, Spelling: "i64"}
	clU64 = ast.NumberType{Width: 64, Signed: false, Spelling: "u64"}
	clPtr = ast.NumberType{Width: ast.WidthPtr, Signed: false, Spelling: "usize"}
)

// clayoutLoad is the value of type t at addr.
func clayoutLoad(t ast.Type, addr ast.Expr) ast.Expr {
	switch t.String() {
	case "u8":
		return clCast(clCall("__load_u8", addr), clU8)
	case "i32":
		return clCall("__load_i32", addr)
	case "u32":
		return clCast(clCall("__load_i32", addr), clU32)
	case "f32":
		return clCall("f32_from_bits", clCall("__load_i32", addr))
	case "i64":
		return clCall("__load_i64", addr)
	case "u64":
		return clCast(clCall("__load_i64", addr), clU64)
	case "f64":
		return clCall("f64_from_bits", clCall("__load_i64", addr))
	}
	return clCall("__load_ptr", addr)
}

// clayoutStore is the statement that writes v of type t at addr.
func clayoutStore(t ast.Type, addr, v ast.Expr) ast.Stmt {
	var call ast.Expr
	switch t.String() {
	case "u8":
		call = clCall("__store_u8", addr, clCast(v, clI32))
	case "i32":
		call = clCall("__store_i32", addr, v)
	case "u32":
		call = clCall("__store_i32", addr, clCast(v, clI32))
	case "f32":
		call = clCall("__store_i32", addr, clCall("f32_bits", v))
	case "i64":
		call = clCall("__store_i64", addr, v)
	case "u64":
		call = clCall("__store_i64", addr, clCast(v, clI64))
	case "f64":
		call = clCall("__store_i64", addr, clCall("f64_bits", v))
	default:
		call = clCall("__store_ptr", addr, v)
	}
	return &ast.ExprStmt{Expr: call}
}

func unsafeBody(stmts ...ast.Stmt) *ast.Block {
	return &ast.Block{Stmts: []ast.Stmt{&ast.Block{Stmts: stmts, IsUnsafe: true}}}
}

// synthCLayout makes the functions of @derive(CLayout): c_size, the unsafe
// c_load and c_store of the whole struct, and for each field F its c_offset_F
// and an unsafe c_load_F and c_store_F. The first three implement the trait. The
// parser of compiler/parser.fern makes the same functions in
// synth_struct_clayout.
func synthCLayout(sd *ast.StructDecl, recv ast.StructType) (trait, extra []*ast.FuncDecl) {
	offs := clayoutOffsets(sd)
	pParam := ast.Param{Name: "p", Type: clPtr}
	trait = append(trait, &ast.FuncDecl{
		Name: "c_size", AssocType: recv.Name, ReturnType: clPtr,
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: usizeLit(offs[len(sd.Fields)])}}},
	})
	fields := make([]ast.FieldInit, 0, len(sd.Fields))
	stores := make([]ast.Stmt, 0, len(sd.Fields))
	for i, f := range sd.Fields {
		fields = append(fields, ast.FieldInit{Name: f.Name, Value: clayoutLoad(f.Type, clayoutAddr(offs[i]))})
		stores = append(stores, clayoutStore(f.Type, clayoutAddr(offs[i]), selfField(f.Name)))
		extra = append(extra,
			&ast.FuncDecl{
				Name: "c_offset_" + f.Name, AssocType: recv.Name, ReturnType: clPtr,
				Body: &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: usizeLit(offs[i])}}},
			},
			&ast.FuncDecl{
				Name: "c_load_" + f.Name, AssocType: recv.Name, Unsafe: true,
				Params: []ast.Param{pParam}, ReturnType: f.Type,
				Body: unsafeBody(&ast.Return{Value: clayoutLoad(f.Type, clayoutAddr(offs[i]))}),
			},
			&ast.FuncDecl{
				Name: "c_store_" + f.Name, AssocType: recv.Name, Unsafe: true,
				Params: []ast.Param{pParam, {Name: "v", Type: f.Type}}, ReturnType: ast.VoidType{},
				Body: unsafeBody(clayoutStore(f.Type, clayoutAddr(offs[i]), &ast.Ident{Name: "v"})),
			})
	}
	lit := &ast.StructLit{TypeName: recv.Name, Fields: fields}
	trait = append(trait,
		&ast.FuncDecl{
			Name: "c_load", AssocType: recv.Name, Unsafe: true,
			Params: []ast.Param{pParam}, ReturnType: recv,
			Body: unsafeBody(&ast.Return{Value: lit}),
		},
		&ast.FuncDecl{
			Name: "c_store", Receiver: &ast.Param{Name: "self", Type: recv}, Unsafe: true,
			Params: []ast.Param{pParam}, ReturnType: ast.VoidType{},
			Body: unsafeBody(stores...),
		})
	return trait, extra
}

// deriveCLayout adds the functions of @derive(CLayout) for sd, and the impl of
// the trait dn, or reports why sd has no C layout.
func (c *checker) deriveCLayout(prog *ast.Program, sd *ast.StructDecl, dn string, recv ast.StructType) {
	if gap := clayoutGap(sd); gap != "" {
		c.errfCode(sd.P, "E021", "cannot @derive(%s) for %s: %s", demangle(dn), demangle(sd.Name), gap)
		return
	}
	trait, extra := synthCLayout(sd, recv)
	names := make([]string, 0, len(trait))
	for _, m := range trait {
		m.SourceModule = sd.SourceModule
		m.ImplTrait = dn
		names = append(names, m.Name)
		prog.Funcs = append(prog.Funcs, m)
	}
	for _, m := range extra {
		m.SourceModule = sd.SourceModule
		prog.Funcs = append(prog.Funcs, m)
	}
	prog.Impls = append(prog.Impls, &ast.ImplDecl{
		P: sd.P, Trait: dn, TraitPos: sd.P, Type: recv, TypePos: sd.P,
		MethodNames: names, SourceModule: sd.SourceModule,
	})
}
