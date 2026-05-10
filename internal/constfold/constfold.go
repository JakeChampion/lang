// Package constfold resolves top-level `const` declarations.
//
// The pass runs after modload (so cross-module references have been
// flattened to mangled names) and before the checker (so the rest of
// the pipeline never sees a ConstDecl). For each const it
// evaluates the initialiser as a constant expression — literals,
// references to earlier consts, and arithmetic / comparison /
// logical / unary operations on those. Anything outside that grammar
// (function calls, array indexing, struct literals, runtime
// expressions) is rejected with a diagnostic.
//
// Once every const is resolved the pass walks the rest of the AST
// and replaces each Ident reference whose name matches a const with
// a literal node carrying the resolved value. Const decls are
// stripped from the program afterwards, so the checker / IR /
// codegen layers stay unaware of the feature.
package constfold

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// Fold evaluates every top-level const declaration in prog, then
// substitutes references with the resolved literal and clears
// prog.Consts. Errors aggregate; the first diagnostic surfaced
// names the offending const and explains why it isn't a valid
// constant expression.
func Fold(prog *ast.Program) error {
	values := map[string]ast.Expr{}
	types := map[string]ast.Type{}
	var errs []error

	for _, cd := range prog.Consts {
		if _, dup := values[cd.Name]; dup {
			errs = append(errs, fmt.Errorf("%s: const %q redeclared", cd.P, cd.Name))
			continue
		}
		val, err := evalConst(cd.Value, values, types)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: const %s: %w", cd.P, cd.Name, err))
			continue
		}
		gotT := litType(val)
		if cd.Type != nil && !ast.Equal(cd.Type, gotT) {
			errs = append(errs, fmt.Errorf("%s: const %s: declared type %s does not match initialiser type %s",
				cd.P, cd.Name, cd.Type, gotT))
			continue
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
	sub := substituter{values: values}
	for _, fn := range prog.Funcs {
		sub.walkBlock(fn.Body)
	}
	prog.Consts = nil
	return nil
}

// evalConst tries to reduce e to a literal AST node using only
// constant-expression rules. Returned values are always one of
// *ast.NumberLit, *ast.FloatLit, *ast.BoolLit, *ast.StringLit.
func evalConst(e ast.Expr, values map[string]ast.Expr, types map[string]ast.Type) (ast.Expr, error) {
	switch n := e.(type) {
	case *ast.NumberLit, *ast.FloatLit, *ast.BoolLit, *ast.StringLit:
		return n, nil
	case *ast.Ident:
		v, ok := values[n.Name]
		if !ok {
			return nil, fmt.Errorf("%q is not a constant (must reference an earlier `const` declaration)", n.Name)
		}
		return v, nil
	case *ast.Unary:
		operand, err := evalConst(n.Operand, values, types)
		if err != nil {
			return nil, err
		}
		return foldUnary(n, operand)
	case *ast.Binary:
		left, err := evalConst(n.Left, values, types)
		if err != nil {
			return nil, err
		}
		right, err := evalConst(n.Right, values, types)
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
		return &ast.NumberLit{P: n.P, Value: l << uint64(r)}, nil
	case ">>":
		return &ast.NumberLit{P: n.P, Value: l >> uint64(r)}, nil
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
// the four scalar literal kinds appear here — everything else would
// have been rejected as non-constant in evalConst.
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
	case *ast.Arena:
		s.walkBlock(x.Body)
	case *ast.If:
		s.walkExpr(&x.Cond)
		s.walkStmt(x.Then)
		if x.Else != nil {
			s.walkStmt(x.Else)
		}
	case *ast.While:
		s.walkExpr(&x.Cond)
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
	case *ast.Switch:
		s.walkExpr(&x.Tag)
		for _, c := range x.Cases {
			for i := range c.Values {
				s.walkExpr(&c.Values[i])
			}
			s.walkBlock(c.Body)
		}
		if x.Default != nil {
			s.walkBlock(x.Default)
		}
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
	case *ast.StructLit:
		for i := range x.Fields {
			s.walkExpr(&x.Fields[i].Value)
		}
	case *ast.FieldAccess:
		s.walkExpr(&x.Target)
	}
}

// cloneLit returns a fresh literal carrying the same scalar value
// as src but a position taken from the substitution site. Doing a
// fresh allocation lets the checker / IR pipeline annotate each
// occurrence independently without aliasing surprises.
func cloneLit(src ast.Expr, pos ast.Position) ast.Expr {
	switch v := src.(type) {
	case *ast.NumberLit:
		return &ast.NumberLit{P: pos, Value: v.Value}
	case *ast.FloatLit:
		return &ast.FloatLit{P: pos, Value: v.Value}
	case *ast.BoolLit:
		return &ast.BoolLit{P: pos, Value: v.Value}
	case *ast.StringLit:
		return &ast.StringLit{P: pos, Value: v.Value}
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
