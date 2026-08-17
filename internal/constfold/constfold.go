// Package constfold resolves top-level `const` declarations.
//
// The pass runs after modload (so cross-module references have been
// flattened to mangled names) and before the checker (so the rest of
// the pipeline never sees a ConstDecl). For each const it
// evaluates the initialiser as a constant expression — literals,
// references to earlier consts, and arithmetic / comparison /
// logical / unary operations on those, plus the __fern_asset builtin
// below. Anything outside that grammar (ordinary function calls, array
// indexing, struct literals, runtime expressions) is rejected with a
// diagnostic.
//
// Once every const is resolved the pass walks the rest of the AST
// and replaces each Ident reference whose name matches a const with
// a literal node carrying the resolved value. Const decls are
// stripped from the program afterwards, so the checker / IR /
// codegen layers stay unaware of the feature.
//
// The same traversal resolves `__fern_asset("name")` against the
// compile-time asset set (see internal/embed), replacing the call with a
// string literal holding the file's bytes. Assets ride the const machinery
// rather than a pass of their own for two reasons: an asset IS a
// compile-time constant, and a `const PAGE: string = __fern_asset(...)`
// has to resolve during const evaluation, not after it.
package constfold

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/embed"
)

// assetBuiltin is the compile-time asset accessor. It is not a function —
// no declaration exists anywhere — so every occurrence must be substituted
// here or reported; anything left behind reaches the checker as an
// undefined identifier.
const assetBuiltin = "__fern_asset"

// assetsBuiltin enumerates the whole bundle: `__fern_assets()` becomes an
// array of `(name, contents)` tuples in sorted-name order. Same
// substitution trick as assetBuiltin one level up, so the backends stay
// unaware of it too.
const assetsBuiltin = "__fern_assets"

// Fold evaluates every top-level const declaration in prog, then
// substitutes references with the resolved literal and clears
// prog.Consts. Errors aggregate; the first diagnostic surfaced
// names the offending const and explains why it isn't a valid
// constant expression.
//
// assets carries the `-embed` bundle and may be nil, in which case any use
// of __fern_asset is itself the error.
func Fold(prog *ast.Program, assets *embed.Set) error {
	values := map[string]ast.Expr{}
	types := map[string]ast.Type{}
	var errs []error

	for _, cd := range prog.Consts {
		if _, dup := values[cd.Name]; dup {
			errs = append(errs, fmt.Errorf("%s: const %q redeclared", cd.P, cd.Name))
			continue
		}
		val, err := evalConst(cd.Value, values, types, assets)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: const %s: %w", cd.P, cd.Name, err))
			continue
		}
		gotT := litType(val)
		if cd.Type != nil {
			if err := settleConstLit(cd.Type, val); err != nil {
				errs = append(errs, fmt.Errorf("%s: const %s: %w", cd.P, cd.Name, err))
				continue
			}
			// The declared type is the const's type once the literal has
			// settled to it — not litType's default reading of the literal.
			gotT = cd.Type
		}
		values[cd.Name] = val
		types[cd.Name] = gotT
	}

	if len(errs) > 0 {
		return joinErrs(errs)
	}

	// Substitute every Ident reference matching a const name with
	// the resolved literal. Const decls are then dropped — the rest
	// of the pipeline runs against a const-free program.
	sub := substituter{values: values, assets: assets}
	for _, fn := range prog.Funcs {
		sub.walkBlock(fn.Body)
	}
	prog.Consts = nil
	if len(sub.errs) > 0 {
		return joinErrs(sub.errs)
	}
	return nil
}

// evalConst tries to reduce e to a literal AST node using only
// constant-expression rules. Returned values are always one of
// *ast.NumberLit, *ast.FloatLit, *ast.BoolLit, *ast.StringLit.
func evalConst(e ast.Expr, values map[string]ast.Expr, types map[string]ast.Type, assets *embed.Set) (ast.Expr, error) {
	switch n := e.(type) {
	case *ast.NumberLit, *ast.FloatLit, *ast.BoolLit, *ast.StringLit, *ast.CharLit:
		return n, nil
	case *ast.Call:
		// A single asset is a string, so it is legal in a const
		// initialiser. The enumeration is not: evalConst's contract is to
		// return a scalar literal, and an array of tuples is neither —
		// settleConstLit and every fold rule below would have to grow a
		// composite case for a value no const can usefully hold.
		if isAssetsCall(n) {
			return nil, fmt.Errorf("%s() builds an array, which is not a constant expression — assign it to a `var` instead", assetsBuiltin)
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
		operand, err := evalConst(n.Operand, values, types, assets)
		if err != nil {
			return nil, err
		}
		return foldUnary(n, operand)
	case *ast.Binary:
		left, err := evalConst(n.Left, values, types, assets)
		if err != nil {
			return nil, err
		}
		right, err := evalConst(n.Right, values, types, assets)
		if err != nil {
			return nil, err
		}
		return foldBinary(n, left, right)
	default:
		return nil, fmt.Errorf("expression is not a constant (only literals, earlier consts, and arithmetic / comparison / logical operations on them are allowed)")
	}
}

// foldUnary reduces -x or !x where x is a literal. Position is
// preserved from the source unary so diagnostics still point at the
// right column if a later layer reports on the resulting node.
func foldUnary(n *ast.Unary, operand ast.Expr) (ast.Expr, error) {
	switch n.Op {
	case "-":
		switch v := operand.(type) {
		case *ast.NumberLit:
			return &ast.NumberLit{P: n.P, Value: -v.Value}, nil
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

// foldBinary handles arithmetic / comparison / logical / string-
// concat over constant literals. It only accepts operand pairs that
// match in scalar shape — number+number, float+float, bool&&bool,
// string+string, etc. Mixed types (e.g. number + float) need an
// explicit conversion in the source, just like at runtime.
func foldBinary(n *ast.Binary, left, right ast.Expr) (ast.Expr, error) {
	// String concatenation: "a" + "b". Comparison on strings is
	// allowed too (== / !=).
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

	// Boolean logic / equality.
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

	// Float arithmetic / comparison.
	if lf, lok := left.(*ast.FloatLit); lok {
		rf, rok := right.(*ast.FloatLit)
		if !rok {
			return nil, fmt.Errorf("binary `%s` mixes float with non-float; conversions must be explicit even in constants", n.Op)
		}
		return foldFloatBinary(n, lf.Value, rf.Value)
	}

	// Number arithmetic / comparison — the default integer path.
	ln, lok := left.(*ast.NumberLit)
	rn, rok := right.(*ast.NumberLit)
	if !lok || !rok {
		return nil, fmt.Errorf("binary `%s` operands aren't both numbers", n.Op)
	}
	return foldNumberBinary(n, ln.Value, rn.Value)
}

// foldNumberBinary handles every operator the language defines on
// integers, returning either a NumberLit or BoolLit depending on
// the operator. Division and modulo by zero are caught here so the
// program never compiles with a poison value baked in.
//
// A shift masks its count to the operand width, and the pass runs before the
// checker so no width is known yet — it folds in int64 throughout (which is
// why `1 << 32` folds to 4294967296 and is then rejected for an i32 const
// rather than wrapping). The shifts therefore mask to 63, the i64 rule. Go
// would instead yield 0 for any count at or above the width and for every
// negative count, so `const A: i32 = 1 << 64` folded to 0 where the same
// expression evaluates to 1 at runtime.
func foldNumberBinary(n *ast.Binary, l, r int64) (ast.Expr, error) {
	switch n.Op {
	case "+":
		return &ast.NumberLit{P: n.P, Value: l + r}, nil
	case "-":
		return &ast.NumberLit{P: n.P, Value: l - r}, nil
	case "*":
		return &ast.NumberLit{P: n.P, Value: l * r}, nil
	case "/":
		if r == 0 {
			return nil, fmt.Errorf("division by zero in constant expression")
		}
		return &ast.NumberLit{P: n.P, Value: l / r}, nil
	case "%":
		if r == 0 {
			return nil, fmt.Errorf("modulo by zero in constant expression")
		}
		return &ast.NumberLit{P: n.P, Value: l % r}, nil
	case "&":
		return &ast.NumberLit{P: n.P, Value: l & r}, nil
	case "|":
		return &ast.NumberLit{P: n.P, Value: l | r}, nil
	case "^":
		return &ast.NumberLit{P: n.P, Value: l ^ r}, nil
	case "<<":
		return &ast.NumberLit{P: n.P, Value: l << (uint64(r) & 63)}, nil
	case ">>":
		return &ast.NumberLit{P: n.P, Value: l >> (uint64(r) & 63)}, nil
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

// litType returns the ast.Type that matches a folded literal. Only
// the scalar literal kinds appear here — everything else would
// have been rejected as non-constant in evalConst.
// settleConstLit checks a const's resolved literal against its declared type
// and, when they agree, stamps the declared width onto the literal node.
//
// It replaces a bare `ast.Equal(declared, litType(val))` comparison, which was
// wrong for every numeric const whose declared width was not the literal's
// default reading: litType reports `NumberType{}` (i32) for ANY integer literal
// and `FloatType{}` (f32) for any float one, so `const B: i64 = 5000000000` was
// rejected as "declared type i64 does not match initialiser type i32" — as was
// the perfectly ordinary `const B: i64 = 5` — and `const H: f64 = 3.5` as
// "does not match initialiser type f32". (ast.Equal claims in its NumberType
// doc comment to treat Polymorphic as a match-anything wildcard, but it does
// not; it compares NormalWidth and signedness.)
//
// Stamping matters as much as accepting: the substituter inlines this literal
// node at every reference, so without a width the i64 const would reach the IR
// as an `i32.const` and the f64 one as an f32. Fixes #5477.
func settleConstLit(want ast.Type, val ast.Expr) error {
	switch w := want.(type) {
	case ast.NumberType:
		// usize (WidthPtr) has no fixed width here — leave it to the old
		// exact-match path rather than range-check against an unknown width.
		if w.IsPointerWidth() {
			break
		}
		lit, ok := val.(*ast.NumberLit)
		if !ok {
			break
		}
		if !intFits(lit.Value, w.NormalWidth(), w.IsSigned()) {
			return fmt.Errorf("initialiser %d is out of range for %s", lit.Value, want)
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
	}
	if got := litType(val); !ast.Equal(want, got) {
		return fmt.Errorf("declared type %s does not match initialiser type %s", want, got)
	}
	return nil
}

// intFits reports whether v is representable in a `width`-bit integer of the
// given signedness. A u64's upper half is unrepresentable in the int64 the
// literal is parsed into, so an unsigned 64-bit type accepts any non-negative
// value.
func intFits(v int64, width int, signed bool) bool {
	if !signed && v < 0 {
		return false
	}
	switch width {
	case 8:
		if signed {
			return v >= -128 && v <= 127
		}
		return v <= 255
	case 16:
		if signed {
			return v >= -32768 && v <= 32767
		}
		return v <= 65535
	case 32:
		if signed {
			return v >= -2147483648 && v <= 2147483647
		}
		return v <= 4294967295
	}
	return true // 64-bit: any int64 value fits (u64's high half is unreachable here)
}

func litType(e ast.Expr) ast.Type {
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

// substituter walks the post-modload AST replacing Ident nodes
// whose name matches a folded const with a fresh copy of the const's
// literal. The literal nodes are immutable so sharing pointers
// would be safe, but the checker will eventually annotate Binary /
// Unary nodes with downstream metadata; cloning the literal keeps
// each substitution position independent.
type substituter struct {
	values map[string]ast.Expr
	assets *embed.Set
	errs   []error
}

// isAssetCall reports whether c is a call of the __fern_asset builtin. It
// matches on the callee name alone so that a malformed use (wrong arity, a
// computed name) is still recognised and reported as such, rather than
// falling through to the checker as a call to an undefined function.
func isAssetCall(c *ast.Call) bool {
	id, ok := c.Callee.(*ast.Ident)
	return ok && id.Name == assetBuiltin
}

// resolveAsset turns one __fern_asset("name") call into the string literal
// holding that asset's bytes.
//
// The name must be a literal: the substitution happens at compile time, so
// there is no later point at which a computed name could be resolved. Saying
// that plainly beats letting it reach the checker as an undefined function.
func resolveAsset(c *ast.Call, assets *embed.Set) (ast.Expr, error) {
	if len(c.Args) != 1 {
		return nil, fmt.Errorf("%s: %s takes exactly one argument, got %d", c.P, assetBuiltin, len(c.Args))
	}
	lit, ok := c.Args[0].(*ast.StringLit)
	if !ok {
		return nil, fmt.Errorf("%s: %s needs a string literal — assets are resolved at compile time, so the name cannot be computed", c.P, assetBuiltin)
	}
	if assets == nil {
		return nil, fmt.Errorf("%s: %s(%q) but no assets were embedded — pass -embed DIR to the compiler", c.P, assetBuiltin, lit.Value)
	}
	data, ok := assets.Lookup(lit.Value)
	if !ok {
		msg := fmt.Sprintf("%s: no embedded asset %q under %s", c.P, lit.Value, assets.Root())
		if did := assets.Suggest(lit.Value); did != "" {
			return nil, fmt.Errorf("%s; did you mean %q?", msg, did)
		}
		return nil, fmt.Errorf("%s (%s)", msg, assets.FormatAvailable())
	}
	return &ast.StringLit{P: c.P, Value: data}, nil
}

// isAssetsCall reports whether c is a call of the __fern_assets builtin.
func isAssetsCall(c *ast.Call) bool {
	id, ok := c.Callee.(*ast.Ident)
	return ok && id.Name == assetsBuiltin
}

// resolveAssets turns `__fern_assets()` into an array literal of
// `(name, contents)` tuples, one per embedded asset.
//
// Order is embed.Set.Names(), which is sorted, so the emitted program is
// byte-identical across hosts — a filesystem-order walk would not be.
//
// Every asset lands in the binary whether or not any __fern_asset call
// names it. That is inherent to enumeration rather than an oversight: the
// point is to reach assets whose names the program does not know.
func resolveAssets(c *ast.Call, assets *embed.Set) (ast.Expr, error) {
	if len(c.Args) != 0 {
		return nil, fmt.Errorf("%s: %s takes no arguments, got %d", c.P, assetsBuiltin, len(c.Args))
	}
	if assets == nil {
		return nil, fmt.Errorf("%s: %s() but no assets were embedded — pass -embed DIR to the compiler", c.P, assetsBuiltin)
	}
	names := assets.Names()
	elems := make([]ast.Expr, 0, len(names))
	for _, n := range names {
		data, _ := assets.Lookup(n)
		elems = append(elems, &ast.TupleLit{P: c.P, Elems: []ast.Expr{
			&ast.StringLit{P: c.P, Value: n},
			&ast.StringLit{P: c.P, Value: data},
		}})
	}
	// ElemType is stamped rather than left for the checker to infer,
	// because an empty bundle produces an empty ArrayLit and the checker
	// rejects that with "E020: empty array literal needs a type
	// annotation" — pointing at whatever consumes the call, not at the
	// call. Embedding a directory that happens to hold no files is
	// legitimate; the loop should just not execute.
	return &ast.ArrayLit{
		P:        c.P,
		Elems:    elems,
		ElemType: ast.TupleType{Elems: []ast.Type{ast.StringType{}, ast.StringType{}}},
	}, nil
}

func (s *substituter) walkBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		s.walkStmt(st)
	}
}

func (s *substituter) walkStmt(st ast.Stmt) {
	switch x := st.(type) {
	case *ast.Block:
		s.walkBlock(x)
	case *ast.If:
		s.walkExpr(&x.Cond)
		s.walkStmt(x.Then)
		if x.Else != nil {
			s.walkStmt(x.Else)
		}
	case *ast.While:
		s.walkExpr(&x.Cond)
		s.walkStmt(x.Body)
	case *ast.Loop:
		s.walkStmt(x.Body)
	case *ast.For:
		if x.Init != nil {
			s.walkStmt(x.Init)
		}
		s.walkExpr(&x.Cond)
		if x.Step != nil {
			s.walkStmt(x.Step)
		}
		s.walkStmt(x.Body)
	case *ast.ForEach:
		s.walkExpr(&x.Iter)
		s.walkStmt(x.Body)
	case *ast.Return:
		if x.Value != nil {
			s.walkExpr(&x.Value)
		}
	case *ast.Var:
		s.walkExpr(&x.Init)
	case *ast.Destructure:
		s.walkExpr(&x.Init)
	case *ast.ExprStmt:
		s.walkExpr(&x.Expr)
	case *ast.FuncDecl:
		s.walkBlock(x.Body)
	}
}

func (s *substituter) walkExpr(slot *ast.Expr) {
	if slot == nil || *slot == nil {
		return
	}
	switch x := (*slot).(type) {
	case *ast.Ident:
		if v, ok := s.values[x.Name]; ok {
			*slot = cloneLit(v, x.P)
		}
	case *ast.Call:
		if isAssetCall(x) {
			lit, err := resolveAsset(x, s.assets)
			if err != nil {
				s.errs = append(s.errs, err)
				return
			}
			*slot = lit
			return
		}
		if isAssetsCall(x) {
			arr, err := resolveAssets(x, s.assets)
			if err != nil {
				s.errs = append(s.errs, err)
				return
			}
			*slot = arr
			return
		}
		s.walkExpr(&x.Callee)
		for i := range x.Args {
			s.walkExpr(&x.Args[i])
		}
	case *ast.Binary:
		s.walkExpr(&x.Left)
		s.walkExpr(&x.Right)
	case *ast.Unary:
		s.walkExpr(&x.Operand)
	case *ast.Index:
		s.walkExpr(&x.Array)
		s.walkExpr(&x.Idx)
	case *ast.ArrayLit:
		for i := range x.Elems {
			s.walkExpr(&x.Elems[i])
		}
	case *ast.Assign:
		s.walkExpr(&x.Target)
		s.walkExpr(&x.Value)
	case *ast.IfExpr:
		s.walkExpr(&x.Cond)
		s.walkExpr(&x.Then)
		s.walkExpr(&x.Else)
	case *ast.TryOp:
		s.walkExpr(&x.Inner)
	case *ast.MatchExpr:
		s.walkExpr(&x.Tag)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				s.walkExpr(&arm.Guard)
			}
			s.walkExpr(&arm.Body)
		}
	case *ast.BlockExpr:
		for _, st := range x.Stmts {
			s.walkStmt(st)
		}
		if x.Tail != nil {
			s.walkExpr(&x.Tail)
		}
	case *ast.StructLit:
		for i := range x.Fields {
			s.walkExpr(&x.Fields[i].Value)
		}
	case *ast.FieldAccess:
		s.walkExpr(&x.Target)
	// The forms below were missing, so a const referenced inside any of them
	// was never substituted and reached the checker as a bare Ident — "E001:
	// undefined identifier" for a const that is plainly in scope. `N as i32`
	// was the common one (#5477); the rest are the same hole in the other
	// compound expressions.
	case *ast.CastExpr:
		s.walkExpr(&x.Inner)
	case *ast.DowncastExpr:
		s.walkExpr(&x.Inner)
	case *ast.SliceExpr:
		s.walkExpr(&x.Source)
		if x.Low != nil {
			s.walkExpr(&x.Low)
		}
		if x.High != nil {
			s.walkExpr(&x.High)
		}
	case *ast.TupleLit:
		for i := range x.Elems {
			s.walkExpr(&x.Elems[i])
		}
	case *ast.MapLit:
		for i := range x.Entries {
			s.walkExpr(&x.Entries[i].Key)
			s.walkExpr(&x.Entries[i].Value)
		}
	case *ast.EnumLit:
		for i := range x.Args {
			s.walkExpr(&x.Args[i])
		}
	case *ast.FString:
		for i := range x.Parts {
			if x.Parts[i].Expr != nil {
				s.walkExpr(&x.Parts[i].Expr)
			}
		}
		if x.Desugared != nil {
			s.walkExpr(&x.Desugared)
		}
	case *ast.Lambda:
		s.walkBlock(x.Body)
	}
}

// cloneLit returns a fresh literal carrying the same scalar value
// as src but a position taken from the substitution site. Doing a
// fresh allocation lets the checker / IR pipeline annotate each
// occurrence independently without aliasing surprises.
func cloneLit(src ast.Expr, pos ast.Position) ast.Expr {
	switch v := src.(type) {
	case *ast.NumberLit:
		// Width / IsUnsigned carry the declared type settleConstLit stamped on
		// the const's literal; dropping them here put every substitution site
		// back at the i32 default, so `const B: i64 = 5000000000` reached the
		// checker as an i32 literal and failed E047 "does not fit in i32".
		return &ast.NumberLit{P: pos, Value: v.Value, Width: v.Width, IsUnsigned: v.IsUnsigned}
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

// joinErrs collapses multiple folding errors into a single error
// whose message lists every problem on its own line. The shape
// mirrors checker.diag.Errors so the driver can format it the same
// way.
func joinErrs(errs []error) error {
	if len(errs) == 1 {
		return errs[0]
	}
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return fmt.Errorf("%s", strings.Join(parts, "\n"))
}
