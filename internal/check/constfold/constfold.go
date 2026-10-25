package constfold

import (
	"fmt"
	"unicode/utf8"

	"github.com/jakechampion/lang/internal/pkg/embed"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

const (
	assetBuiltin       = "__fern_asset"
	assetBytesBuiltin  = "__fern_asset_bytes"
	assetsBuiltin      = "__fern_assets"
	assetsBytesBuiltin = "__fern_assets_bytes"
)

const targetOSBuiltin = "target_os"

const targetArchBuiltin = "target_arch"

type Inputs struct {
	Assets     *embed.Set
	TargetOS   string
	TargetArch string
}

func Fold(prog *ast.Program, assets *embed.Set) error {
	return FoldWith(prog, Inputs{Assets: assets})
}

func FoldWith(prog *ast.Program, in Inputs) error {
	values := map[string]ast.Expr{}
	types := map[string]ast.Type{}
	var errs []error

	for _, cd := range prog.Consts {
		if _, dup := values[cd.Name]; dup {
			errs = append(errs, &Error{Pos: cd.P, Path: cd.SourceModule, Msg: fmt.Sprintf("const %q redeclared", cd.Name), ErrCode: "E006"})
			continue
		}
		if rangeErrs := checkLiteralRange(cd); len(rangeErrs) > 0 {
			errs = append(errs, rangeErrs...)
			continue
		}
		val, err := evalConstTyped(cd.Value, cd.Type, values, types, in.Assets)
		if err != nil {
			errs = append(errs, constError(cd, err))
			continue
		}
		gotT := litType(val)
		if cd.Type != nil {
			if err := settleConstLit(cd.Type, val); err != nil {
				errs = append(errs, constError(cd, err))
				continue
			}
			gotT = cd.Type
		}
		values[cd.Name] = val
		types[cd.Name] = gotT
	}

	if len(errs) > 0 {
		return diag.Errors(errs)
	}

	libValues := map[string]ast.Expr{}
	for _, cd := range prog.Consts {
		if v, ok := values[cd.Name]; ok && (prog.EntryModule == "" || cd.SourceModule != prog.EntryModule) {
			libValues[cd.Name] = v
		}
	}
	sub := substituter{values: values, assets: in.Assets, targetOS: in.TargetOS, targetArch: in.TargetArch}
	foldFunc := func(fn *ast.FuncDecl) {
		sub.values = values
		sub.module = fn.BodyModule()
		if body := fn.BodyModule(); body != "" && body != prog.EntryModule {
			sub.values = libValues
		}
		w := ast.ScopeWalker{Visit: sub.visit, AfterStmt: foldIfCond}
		w.WalkFunc(fn)
	}
	for _, fn := range prog.Funcs {
		foldFunc(fn)
	}
	for _, td := range prog.Traits {
		for i := range td.Methods {
			method := &td.Methods[i]
			if method.Body != nil {
				foldFunc(&ast.FuncDecl{P: method.P, Params: method.Params, Body: method.Body, SourceModule: td.SourceModule})
			}
		}
	}
	prog.Consts = nil
	if len(sub.errs) > 0 {
		return diag.Errors(sub.errs)
	}
	return nil
}

func evalConstTyped(e ast.Expr, want ast.Type, values map[string]ast.Expr, types map[string]ast.Type, assets *embed.Set) (ast.Expr, error) {
	switch n := e.(type) {
	case *ast.ArrayLit:
		var elemT ast.Type
		if at, ok := want.(ast.ArrayType); ok {
			elemT = at.Elem
		}
		elems := make([]ast.Expr, len(n.Elems))
		for i, el := range n.Elems {
			v, err := evalConstTyped(el, elemT, values, types, assets)
			if err != nil {
				return nil, err
			}
			elems[i] = v
		}
		return &ast.ArrayLit{P: n.P, Elems: elems, ElemType: n.ElemType}, nil
	case *ast.TupleLit:
		var elemTs []ast.Type
		if tt, ok := want.(ast.TupleType); ok && len(tt.Elems) == len(n.Elems) {
			elemTs = tt.Elems
		}
		elems := make([]ast.Expr, len(n.Elems))
		for i, el := range n.Elems {
			var et ast.Type
			if elemTs != nil {
				et = elemTs[i]
			}
			v, err := evalConstTyped(el, et, values, types, assets)
			if err != nil {
				return nil, err
			}
			elems[i] = v
		}
		return &ast.TupleLit{P: n.P, Elems: elems}, nil
	case *ast.StructLit:
		if n.Base != nil {
			break
		}
		fields := make([]ast.FieldInit, len(n.Fields))
		for i, f := range n.Fields {
			v, err := evalConstTyped(f.Value, nil, values, types, assets)
			if err != nil {
				return nil, err
			}
			fields[i] = f
			fields[i].Value = v
		}
		return &ast.StructLit{P: n.P, TypeName: n.TypeName, Fields: fields, TypeArgs: n.TypeArgs}, nil
	case *ast.Call:
		if isAssetsCall(n) {
			return resolveAssets(n, assets)
		}
		if isAssetCall(n) && assetBytes(n) {
			return resolveAsset(n, assets)
		}
	case *ast.Ident:
		if v, ok := values[n.Name]; ok {
			return cloneLit(v, n.P), nil
		}
	}
	return evalConst(e, declaredWidth(want), values, types, assets)
}

func evalConst(e ast.Expr, w intWidth, values map[string]ast.Expr, types map[string]ast.Type, assets *embed.Set) (ast.Expr, error) {
	switch n := e.(type) {
	case *ast.NumberLit, *ast.FloatLit, *ast.BoolLit, *ast.StringLit, *ast.CharLit:
		return n, nil
	case *ast.Call:
		if isAssetsCall(n) || (isAssetCall(n) && assetBytes(n)) {
			return nil, fmt.Errorf("%s() builds an array, which an operator cannot take in a constant expression", calleeName(n))
		}
		if !isAssetCall(n) {
			return nil, fmt.Errorf("expression is not a constant (only literals, earlier consts, and arithmetic / comparison / logical operations on them are allowed)")
		}
		return resolveAsset(n, assets)
	case *ast.Ident:
		v, ok := values[n.Name]
		if !ok {
			return nil, fmt.Errorf("%q is not a constant (must reference an earlier `const` declaration)", n.Name)
		}
		return v, nil
	case *ast.Unary:
		operand, err := evalConst(n.Operand, w, values, types, assets)
		if err != nil {
			return nil, err
		}
		return foldUnary(n, w, operand)
	case *ast.Binary:
		left, err := evalConst(n.Left, w, values, types, assets)
		if err != nil {
			return nil, err
		}
		right, err := evalConst(n.Right, w, values, types, assets)
		if err != nil {
			return nil, err
		}
		return foldBinary(n, w, left, right)
	default:
		return nil, fmt.Errorf("expression is not a constant (only literals, earlier consts, and arithmetic / comparison / logical operations on them are allowed)")
	}
}

func foldUnary(n *ast.Unary, w intWidth, operand ast.Expr) (ast.Expr, error) {
	switch n.Op {
	case "-":
		switch v := operand.(type) {
		case *ast.NumberLit:
			return &ast.NumberLit{P: n.P, Value: w.trunc(-v.Value)}, nil
		case *ast.FloatLit:
			return &ast.FloatLit{P: n.P, Value: -v.Value}, nil
		}
		return nil, fmt.Errorf("unary `-` requires a number or float operand")
	case "!":
		v, ok := operand.(*ast.BoolLit)
		if !ok {
			return nil, fmt.Errorf("unary `!` requires a boolean operand")
		}
		return &ast.BoolLit{P: n.P, Value: !v.Value}, nil
	}
	return nil, fmt.Errorf("unary operator %q is not allowed in constant expressions", n.Op)
}

func foldBinary(n *ast.Binary, w intWidth, left, right ast.Expr) (ast.Expr, error) {
	if ls, lok := left.(*ast.StringLit); lok {
		rs, rok := right.(*ast.StringLit)
		if !rok {
			return nil, fmt.Errorf("binary `%s` between string and non-string is not a constant expression", n.Op)
		}
		switch n.Op {
		case "+":
			return &ast.StringLit{P: n.P, Value: ls.Value + rs.Value}, nil
		case "==":
			return &ast.BoolLit{P: n.P, Value: ls.Value == rs.Value}, nil
		case "!=":
			return &ast.BoolLit{P: n.P, Value: ls.Value != rs.Value}, nil
		}
		return nil, fmt.Errorf("operator `%s` not allowed on strings in constant expressions", n.Op)
	}

	if lb, lok := left.(*ast.BoolLit); lok {
		rb, rok := right.(*ast.BoolLit)
		if !rok {
			return nil, fmt.Errorf("binary `%s` between bool and non-bool is not a constant expression", n.Op)
		}
		switch n.Op {
		case "&&":
			return &ast.BoolLit{P: n.P, Value: lb.Value && rb.Value}, nil
		case "||":
			return &ast.BoolLit{P: n.P, Value: lb.Value || rb.Value}, nil
		case "==":
			return &ast.BoolLit{P: n.P, Value: lb.Value == rb.Value}, nil
		case "!=":
			return &ast.BoolLit{P: n.P, Value: lb.Value != rb.Value}, nil
		}
		return nil, fmt.Errorf("operator `%s` not allowed on bools in constant expressions", n.Op)
	}

	if lf, lok := left.(*ast.FloatLit); lok {
		rf, rok := right.(*ast.FloatLit)
		if !rok {
			return nil, fmt.Errorf("binary `%s` mixes float with non-float; conversions must be explicit even in constants", n.Op)
		}
		return foldFloatBinary(n, lf.Value, rf.Value)
	}

	ln, lok := left.(*ast.NumberLit)
	rn, rok := right.(*ast.NumberLit)
	if !lok || !rok {
		return nil, fmt.Errorf("binary `%s` operands aren't both numbers", n.Op)
	}
	return foldNumberBinary(n, w, ln.Value, rn.Value)
}

type intWidth struct {
	bits   int
	signed bool
}

func declaredWidth(t ast.Type) intWidth {
	n, ok := t.(ast.NumberType)
	if !ok || n.IsPointerWidth() || n.Polymorphic {
		return intWidth{}
	}
	return intWidth{bits: n.NormalWidth(), signed: n.IsSigned()}
}

func (w intWidth) known() bool { return w.bits != 0 }

func (w intWidth) unsigned() bool { return w.known() && !w.signed }

func (w intWidth) trunc(v int64) int64 {
	if !w.known() || w.bits >= 64 {
		return v
	}
	shift := uint(64 - w.bits)
	if w.signed {
		return v << shift >> shift
	}
	return int64(uint64(v) << shift >> shift)
}

func (w intWidth) shiftMask() uint64 {
	if !w.known() || w.bits > 32 {
		return 63
	}
	return 31
}

func foldNumberBinary(n *ast.Binary, w intWidth, l, r int64) (ast.Expr, error) {
	num := func(v int64) (ast.Expr, error) {
		return &ast.NumberLit{P: n.P, Value: w.trunc(v)}, nil
	}
	count := uint64(r) & w.shiftMask()
	switch n.Op {
	case "+":
		return num(l + r)
	case "-":
		return num(l - r)
	case "*":
		return num(l * r)
	case "/":
		if r == 0 {
			return nil, fmt.Errorf("division by zero in constant expression")
		}
		if w.unsigned() {
			return num(int64(uint64(l) / uint64(r)))
		}
		return num(l / r)
	case "%":
		if r == 0 {
			return nil, fmt.Errorf("modulo by zero in constant expression")
		}
		if w.unsigned() {
			return num(int64(uint64(l) % uint64(r)))
		}
		return num(l % r)
	case "&":
		return num(l & r)
	case "|":
		return num(l | r)
	case "^":
		return num(l ^ r)
	case "<<":
		return num(l << count)
	case ">>":
		if w.unsigned() {
			return num(int64(uint64(l) >> count))
		}
		return num(l >> count)
	case "==":
		return &ast.BoolLit{P: n.P, Value: l == r}, nil
	case "!=":
		return &ast.BoolLit{P: n.P, Value: l != r}, nil
	case "<":
		if w.unsigned() {
			return &ast.BoolLit{P: n.P, Value: uint64(l) < uint64(r)}, nil
		}
		return &ast.BoolLit{P: n.P, Value: l < r}, nil
	case "<=":
		if w.unsigned() {
			return &ast.BoolLit{P: n.P, Value: uint64(l) <= uint64(r)}, nil
		}
		return &ast.BoolLit{P: n.P, Value: l <= r}, nil
	case ">":
		if w.unsigned() {
			return &ast.BoolLit{P: n.P, Value: uint64(l) > uint64(r)}, nil
		}
		return &ast.BoolLit{P: n.P, Value: l > r}, nil
	case ">=":
		if w.unsigned() {
			return &ast.BoolLit{P: n.P, Value: uint64(l) >= uint64(r)}, nil
		}
		return &ast.BoolLit{P: n.P, Value: l >= r}, nil
	}
	return nil, fmt.Errorf("operator `%s` not allowed in integer constant expressions", n.Op)
}

func foldFloatBinary(n *ast.Binary, l, r float64) (ast.Expr, error) {
	switch n.Op {
	case "+":
		return &ast.FloatLit{P: n.P, Value: l + r}, nil
	case "-":
		return &ast.FloatLit{P: n.P, Value: l - r}, nil
	case "*":
		return &ast.FloatLit{P: n.P, Value: l * r}, nil
	case "/":
		if r == 0 {
			return nil, fmt.Errorf("division by zero in constant expression")
		}
		return &ast.FloatLit{P: n.P, Value: l / r}, nil
	case "==":
		return &ast.BoolLit{P: n.P, Value: l == r}, nil
	case "!=":
		return &ast.BoolLit{P: n.P, Value: l != r}, nil
	case "<":
		return &ast.BoolLit{P: n.P, Value: l < r}, nil
	case "<=":
		return &ast.BoolLit{P: n.P, Value: l <= r}, nil
	case ">":
		return &ast.BoolLit{P: n.P, Value: l > r}, nil
	case ">=":
		return &ast.BoolLit{P: n.P, Value: l >= r}, nil
	}
	return nil, fmt.Errorf("operator `%s` not allowed in float constant expressions", n.Op)
}

func constError(cd *ast.ConstDecl, err error) error {
	msg, code := err.Error(), "E093"
	if e, ok := err.(*Error); ok {
		msg = e.Msg
		if e.ErrCode != "" {
			code = e.ErrCode
		}
	}
	return &Error{Pos: cd.P, Path: cd.SourceModule, Msg: "const " + cd.Name + ": " + msg, ErrCode: code}
}

func checkLiteralRange(cd *ast.ConstDecl) []error {
	declared, hasType := cd.Type.(ast.NumberType)
	var errs []error
	walkIntLits(cd.Value, false, true, func(lit *ast.NumberLit, negated, typed bool) {
		t := declared
		switch {
		case lit.Width != 0 && !lit.IsFloat:
			t = ast.NumberType{Width: lit.Width, Signed: !lit.IsUnsigned}
		case typed && hasType:
		case lit.ExceedsU64:
			t = ast.NumberType{Width: 64, Signed: true}
		default:
			return
		}
		if msg := ast.IntLitOutOfRange(lit, negated, t); msg != "" {
			errs = append(errs, &Error{Pos: lit.P, Msg: msg, Path: cd.SourceModule, ErrCode: "E047"})
		}
	})
	return errs
}

func walkIntLits(e ast.Expr, negated, typed bool, visit func(lit *ast.NumberLit, negated, typed bool)) {
	switch x := e.(type) {
	case *ast.NumberLit:
		visit(x, negated, typed)
	case *ast.Unary:
		switch x.Op {
		case "-":
			walkIntLits(x.Operand, !negated, typed, visit)
		case "+":
			walkIntLits(x.Operand, negated, typed, visit)
		default:
			walkIntLits(x.Operand, false, false, visit)
		}
	case *ast.Binary:
		switch x.Op {
		case "+", "-", "*", "/", "%", "&", "|", "^", "<<", ">>":
		default:
			typed = false
		}
		walkIntLits(x.Left, false, typed, visit)
		walkIntLits(x.Right, false, typed, visit)
	}
}

func settleConstLit(want ast.Type, val ast.Expr) error {
	switch w := want.(type) {
	case ast.NumberType:
		lit, ok := val.(*ast.NumberLit)
		if !ok {
			break
		}
		lit.Width = w.NormalWidth()
		lit.IsUnsigned = !w.IsSigned()
		return nil
	case ast.FloatType:
		lit, ok := val.(*ast.FloatLit)
		if !ok {
			break
		}
		lit.Width = w.NormalWidth()
		return nil
	case ast.ArrayType:
		lit, ok := val.(*ast.ArrayLit)
		if !ok {
			break
		}
		for _, el := range lit.Elems {
			if err := settleConstLit(w.Elem, el); err != nil {
				return err
			}
		}
		lit.ElemType = w.Elem
		return nil
	case ast.TupleType:
		lit, ok := val.(*ast.TupleLit)
		if !ok || len(lit.Elems) != len(w.Elems) {
			break
		}
		for i, el := range lit.Elems {
			if err := settleConstLit(w.Elems[i], el); err != nil {
				return err
			}
		}
		return nil
	case ast.StructType:
		if lit, ok := val.(*ast.StructLit); ok && lit.TypeName == w.Name {
			return nil
		}
	}
	if got := litType(val); !ast.Equal(want, got) {
		return fmt.Errorf("declared type %s does not match initialiser type %s", want, got)
	}
	return nil
}

func litType(e ast.Expr) ast.Type {
	switch x := e.(type) {
	case *ast.ArrayLit:
		if x.ElemType != nil {
			return ast.ArrayType{Elem: x.ElemType}
		}
		if len(x.Elems) == 0 {
			return nil
		}
		return ast.ArrayType{Elem: litType(x.Elems[0])}
	case *ast.TupleLit:
		elems := make([]ast.Type, len(x.Elems))
		for i, el := range x.Elems {
			elems[i] = litType(el)
		}
		return ast.TupleType{Elems: elems}
	}
	switch e.(type) {
	case *ast.NumberLit:
		return ast.NumberType{}
	case *ast.FloatLit:
		return ast.FloatType{}
	case *ast.BoolLit:
		return ast.BoolType{}
	case *ast.StringLit:
		return ast.StringType{}
	case *ast.CharLit:
		if e.(*ast.CharLit).IsByte {
			return ast.NumberType{Width: 8, Signed: false}
		}
		return ast.CharType{}
	}
	return nil
}

type substituter struct {
	values     map[string]ast.Expr
	assets     *embed.Set
	targetOS   string
	targetArch string
	errs       []error
	module     string
}

func foldIfCond(st ast.Stmt) {
	if x, ok := st.(*ast.If); ok {
		x.Cond = foldLiteralCond(x.Cond)
	}
}

func isTargetCall(c *ast.Call, name string) bool {
	id, ok := c.Callee.(*ast.Ident)
	return ok && id.Name == name && len(c.Args) == 0
}

func isAssetCall(c *ast.Call) bool {
	n := calleeName(c)
	return n == assetBuiltin || n == assetBytesBuiltin
}

func isAssetsCall(c *ast.Call) bool {
	n := calleeName(c)
	return n == assetsBuiltin || n == assetsBytesBuiltin
}

func assetBytes(c *ast.Call) bool {
	n := calleeName(c)
	return n == assetBytesBuiltin || n == assetsBytesBuiltin
}

func calleeName(c *ast.Call) string {
	if id, ok := c.Callee.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func resolveAsset(c *ast.Call, assets *embed.Set) (ast.Expr, error) {
	fn := calleeName(c)
	if len(c.Args) != 1 {
		return nil, assetError(c.P, "%s takes exactly one argument, got %d", fn, len(c.Args))
	}
	lit, ok := c.Args[0].(*ast.StringLit)
	if !ok {
		return nil, assetError(c.P, "%s needs a string literal — assets are resolved at compile time, so the name cannot be computed", fn)
	}
	if assets == nil {
		return nil, assetError(c.P, "%s(%q) but no assets were embedded — pass -embed DIR to the compiler", fn, lit.Value)
	}
	data, ok := assets.Lookup(lit.Value)
	if !ok {
		msg := fmt.Sprintf("no embedded asset %q under %s", lit.Value, assets.Root())
		if did := assets.Suggest(lit.Value); did != "" {
			return nil, assetError(c.P, "%s; did you mean %q?", msg, did)
		}
		return nil, assetError(c.P, "%s (%s)", msg, assets.FormatAvailable())
	}
	if assetBytes(c) {
		return bytesLit(c.P, data), nil
	}
	if !utf8.ValidString(data) {
		return nil, notText(c.P, fn, assets, lit.Value)
	}
	return &ast.StringLit{P: c.P, Value: data}, nil
}

func notText(p ast.Position, fn string, assets *embed.Set, name string) error {
	return assetError(p, "%s: embedded asset %q under %s is not valid UTF-8 — read it as u8[] with %s_bytes", fn, name, assets.Root(), fn)
}

func inModule(err error, module string) error {
	if e, ok := err.(*Error); ok && e.Path == "" {
		e.Path = module
	}
	return err
}

func assetError(p ast.Position, format string, args ...any) error {
	return &Error{Pos: p, Msg: fmt.Sprintf(format, args...), ErrCode: "E094"}
}

var u8Type = ast.NumberType{Width: 8, Signed: false}

func bytesLit(p ast.Position, data string) *ast.ArrayLit {
	elems := make([]ast.Expr, len(data))
	for i := 0; i < len(data); i++ {
		elems[i] = &ast.NumberLit{P: p, Value: int64(data[i]), Width: 8, IsUnsigned: true}
	}
	return &ast.ArrayLit{P: p, Elems: elems, ElemType: u8Type}
}

func resolveAssets(c *ast.Call, assets *embed.Set) (ast.Expr, error) {
	fn := calleeName(c)
	if len(c.Args) != 0 {
		return nil, assetError(c.P, "%s takes no arguments, got %d", fn, len(c.Args))
	}
	if assets == nil {
		return nil, assetError(c.P, "%s() but no assets were embedded — pass -embed DIR to the compiler", fn)
	}
	bytes := assetBytes(c)
	var contentT ast.Type = ast.StringType{}
	if bytes {
		contentT = ast.ArrayType{Elem: u8Type}
	}
	names := assets.Names()
	elems := make([]ast.Expr, 0, len(names))
	for _, n := range names {
		data, _ := assets.Lookup(n)
		var content ast.Expr = &ast.StringLit{P: c.P, Value: data}
		if bytes {
			content = bytesLit(c.P, data)
		} else if !utf8.ValidString(data) {
			return nil, notText(c.P, fn, assets, n)
		}
		elems = append(elems, &ast.TupleLit{P: c.P, Elems: []ast.Expr{
			&ast.StringLit{P: c.P, Value: n},
			content,
		}})
	}
	return &ast.ArrayLit{
		P:        c.P,
		Elems:    elems,
		ElemType: ast.TupleType{Elems: []ast.Type{ast.StringType{}, contentT}},
	}, nil
}

func foldLiteralCond(e ast.Expr) ast.Expr {
	switch x := e.(type) {
	case *ast.Binary:
		l := foldLiteralCond(x.Left)
		r := foldLiteralCond(x.Right)
		if isLiteralOperand(l) && isLiteralOperand(r) {
			if v, err := foldBinary(x, intWidth{}, l, r); err == nil {
				return v
			}
		}
	case *ast.Unary:
		o := foldLiteralCond(x.Operand)
		if _, ok := o.(*ast.BoolLit); ok {
			if v, err := foldUnary(x, intWidth{}, o); err == nil {
				return v
			}
		}
	}
	return e
}

func isLiteralOperand(e ast.Expr) bool {
	switch e.(type) {
	case *ast.StringLit, *ast.BoolLit:
		return true
	}
	return false
}

func PruneDeadArms(prog *ast.Program) {
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if x, ok := n.(*ast.If); ok && !x.IsAssert {
			pruneLiteralIf(x)
		}
		return true
	})
}

func pruneLiteralIf(x *ast.If) {
	b, ok := x.Cond.(*ast.BoolLit)
	if !ok {
		return
	}
	if b.Value {
		x.Else = nil
		return
	}
	x.Cond = &ast.BoolLit{P: b.P, Value: true}
	switch live := x.Else.(type) {
	case nil:
		x.Then = &ast.Block{P: x.P}
	case *ast.Block:
		x.Then = live
	default:
		x.Then = &ast.Block{P: x.P, Stmts: []ast.Stmt{live}}
	}
	x.Else = nil
}

func (s *substituter) visit(slot *ast.Expr, w *ast.ScopeWalker) bool {
	switch x := (*slot).(type) {
	case *ast.Ident:
		if v, ok := s.values[x.Name]; ok && !w.IsBound(x.Name) {
			*slot = cloneLit(v, x.P)
		}
		return false
	case *ast.Call:
		if s.targetOS != "" && isTargetCall(x, targetOSBuiltin) {
			*slot = &ast.StringLit{P: x.P, Value: s.targetOS}
			return false
		}
		if s.targetArch != "" && isTargetCall(x, targetArchBuiltin) {
			*slot = &ast.StringLit{P: x.P, Value: s.targetArch}
			return false
		}
		if isAssetCall(x) {
			lit, err := resolveAsset(x, s.assets)
			if err != nil {
				s.errs = append(s.errs, inModule(err, s.module))
				return false
			}
			*slot = lit
			return false
		}
		if isAssetsCall(x) {
			arr, err := resolveAssets(x, s.assets)
			if err != nil {
				s.errs = append(s.errs, inModule(err, s.module))
				return false
			}
			*slot = arr
			return false
		}
	case *ast.Assign:
		if id, ok := x.Target.(*ast.Ident); ok {
			if _, isConst := s.values[id.Name]; isConst && !w.IsBound(id.Name) {
				s.errs = append(s.errs, &Error{Pos: id.P, Path: s.module, Msg: "cannot assign to const " + id.Name, ErrCode: "E091"})
				w.WalkExpr(&x.Value)
				return false
			}
		}
	}
	return true
}

func cloneLit(src ast.Expr, pos ast.Position) ast.Expr {
	switch v := src.(type) {
	case *ast.ArrayLit:
		elems := make([]ast.Expr, len(v.Elems))
		for i, el := range v.Elems {
			elems[i] = cloneLit(el, pos)
		}
		return &ast.ArrayLit{P: pos, Elems: elems, ElemType: v.ElemType, Const: true}
	case *ast.TupleLit:
		elems := make([]ast.Expr, len(v.Elems))
		for i, el := range v.Elems {
			elems[i] = cloneLit(el, pos)
		}
		return &ast.TupleLit{P: pos, Elems: elems}
	case *ast.StructLit:
		fields := make([]ast.FieldInit, len(v.Fields))
		for i, f := range v.Fields {
			fields[i] = f
			fields[i].Value = cloneLit(f.Value, pos)
		}
		return &ast.StructLit{P: pos, TypeName: v.TypeName, Fields: fields, TypeArgs: v.TypeArgs}
	case *ast.NumberLit:
		return &ast.NumberLit{P: pos, Value: v.Value, Raw: v.Raw, Width: v.Width, IsUnsigned: v.IsUnsigned, ExceedsI64: v.ExceedsI64}
	case *ast.FloatLit:
		return &ast.FloatLit{P: pos, Value: v.Value, Width: v.Width}
	case *ast.BoolLit:
		return &ast.BoolLit{P: pos, Value: v.Value}
	case *ast.StringLit:
		return &ast.StringLit{P: pos, Value: v.Value}
	case *ast.CharLit:
		return &ast.CharLit{P: pos, Value: v.Value, Raw: v.Raw, IsByte: v.IsByte}
	}
	return src
}
