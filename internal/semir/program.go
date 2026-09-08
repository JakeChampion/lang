package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ssa"
)

// Program owns a closed set of typed functions. Function identities are
// one-based indices in funcs; source spellings are resolved once by the builder
// and are not used as ownership-analysis keys. The program and its SSA graphs
// remain private to this phase until explicit RC and layout lowering.
type Program struct {
	funcs  []*Func
	byName map[string]int64
}

type funcContract struct {
	params []ast.Type
	modes  []ParamMode
	result ast.Type
}

// BuildProgram constructs signatures before bodies, so forward calls and
// recursion share the same contracts. Every declaration in this explicit
// input program must be supported; an unbuilt callee cannot silently acquire
// an ownership summary. External and indirect calls are not implemented yet.
func BuildProgram(prog *ast.Program, info *checker.Info) (*Program, error) {
	if prog == nil || info == nil {
		return nil, fmt.Errorf("semir: missing checked program")
	}
	p := &Program{byName: make(map[string]int64)}
	for _, decl := range prog.Funcs {
		if decl == nil || decl.Body == nil || len(decl.TypeParams) != 0 {
			return nil, fmt.Errorf("semir: expected a checked, concrete function body")
		}
		if _, exists := p.byName[decl.Name]; exists {
			return nil, fmt.Errorf("semir: duplicate function %q", decl.Name)
		}
		sig := info.FuncSigs[decl.Name]
		if sig == nil || len(sig.Params) != len(decl.Params) || !ast.Equal(sig.Result, decl.ReturnType) {
			return nil, fmt.Errorf("semir %s: missing or inconsistent checked signature", decl.Name)
		}
		f := newFunc(decl.Name, sig.Result)
		f.graph.NewBlock()
		f.program = p
		f.contract = funcContract{params: append([]ast.Type(nil), sig.Params...), result: sig.Result}
		own := info.OwnFuncs[decl.Name]
		if len(own) != 0 && len(own) != len(decl.Params) {
			return nil, fmt.Errorf("semir %s: inconsistent checked ownership contract", decl.Name)
		}
		for i, param := range decl.Params {
			if !ast.Equal(sig.Params[i], param.Type) {
				return nil, fmt.Errorf("semir %s: inconsistent checked parameter type", decl.Name)
			}
			if param.Own != (len(own) != 0 && own[i]) {
				return nil, fmt.Errorf("semir %s: inconsistent checked ownership contract", decl.Name)
			}
			mode := ParamValue
			if referenceBearing(param.Type) {
				mode = ParamBorrow
				if param.Own {
					mode = ParamCounted
				}
			}
			f.contract.modes = append(f.contract.modes, mode)
			f.addParam(param.Type, mode, param.NamePos)
		}
		p.funcs = append(p.funcs, f)
		p.byName[decl.Name] = int64(len(p.funcs))
	}
	for i, decl := range prog.Funcs {
		if err := buildBody(p.funcs[i], decl, info); err != nil {
			return nil, err
		}
	}
	for _, f := range p.funcs {
		if err := expandCleanups(f); err != nil {
			return nil, err
		}
		if err := promoteBindings(f); err != nil {
			return nil, err
		}
	}
	if err := VerifyProgram(p); err != nil {
		return nil, err
	}
	for _, f := range p.funcs {
		if err := finishFlow(f); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// VerifyProgram also checks that each function's actual body parameters match
// the shared contracts that callers used, including own/borrow distinctions.
func VerifyProgram(p *Program) error {
	if p == nil {
		return fmt.Errorf("semir: nil program")
	}
	for _, f := range p.funcs {
		if f == nil || f.program != p {
			return fmt.Errorf("semir: foreign function in program")
		}
		if err := Verify(f); err != nil {
			return err
		}
		c := f.contract
		if !ast.Equal(c.result, f.result) || len(c.params) != len(f.graph.Params) || len(c.modes) != len(f.modes) {
			return fmt.Errorf("semir %s: body disagrees with call contract", f.graph.Name)
		}
		for i, param := range f.graph.Params {
			if !ast.Equal(c.params[i], f.values[param.ID].typ) || c.modes[i] != f.modes[i] {
				return fmt.Errorf("semir %s: parameter disagrees with call contract", f.graph.Name)
			}
		}
	}
	return nil
}

func (f *Func) callee(op *ssa.Op) (*Func, error) {
	if f.program == nil || op.Imm < 1 || op.Imm > int64(len(f.program.funcs)) {
		return nil, fmt.Errorf("unresolved semantic function identity %d", op.Imm)
	}
	callee := f.program.funcs[op.Imm-1]
	if callee == nil || callee.program != f.program || len(callee.contract.params) != len(callee.contract.modes) {
		return nil, fmt.Errorf("invalid semantic call contract")
	}
	c := callee.contract
	if !ast.Equal(c.result, callee.result) || len(c.params) != len(callee.graph.Params) || len(c.modes) != len(callee.modes) {
		return nil, fmt.Errorf("body disagrees with semantic call contract")
	}
	for i, param := range callee.graph.Params {
		typ := callee.values[param.ID].typ
		if !ast.Equal(c.params[i], typ) || c.modes[i] != callee.modes[i] {
			return nil, fmt.Errorf("parameter disagrees with semantic call contract")
		}
		ref := referenceBearing(typ)
		if (ref && c.modes[i] != ParamBorrow && c.modes[i] != ParamCounted) || (!ref && c.modes[i] != ParamValue) {
			return nil, fmt.Errorf("invalid ownership mode in semantic call contract")
		}
	}
	return callee, nil
}
