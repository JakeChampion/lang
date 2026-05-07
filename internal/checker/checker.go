// Package checker performs name-resolution and type-checking on a Program.
//
// Each function is checked against an environment chain that starts at the
// top-level (functions) and is extended for parameters and `var`
// declarations. Errors are accumulated rather than fatally aborting on the
// first one, so a single run reports as much as possible.
package checker

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
)

type Error struct {
	Pos  ast.Position
	Span int    // optional: token length for `^~~~~` underline; 0 = caret only
	Note string // optional: "did you mean foo?" hint
	Msg  string
}

func (e *Error) Error() string          { return fmt.Sprintf("type error at %s: %s", e.Pos, e.Msg) }
func (e *Error) Position() ast.Position { return e.Pos }
func (e *Error) Length() int            { return e.Span }
func (e *Error) Hint() string           { return e.Note }

// Info captures everything codegen needs that the checker discovered:
// the inferred type of every var without an annotation, and a per-function
// list of locals (so codegen can lay out a frame).
type Info struct {
	VarTypes map[*ast.Var]ast.Type
	Locals   map[*ast.FuncDecl][]*ast.Var
	FuncSigs map[string]*ast.FuncType
	// Structs maps a struct name to its declaration (which carries the
	// ordered field list — codegen looks up field offsets here).
	Structs map[string]*ast.StructDecl
	// Enums maps an enum name to its declaration. The variant list +
	// payload types live there; codegen looks up the runtime tag
	// (the variant's index in the variant slice) and the payload
	// shapes via this map.
	Enums map[string]*ast.EnumDecl
	// Methods maps `<StructName>.<MethodName>` to the mangled
	// top-level function name the receiver-rewriting pass introduces
	// (`__method_<StructName>_<MethodName>`). Call-site rewriting
	// uses this map to turn `p.area()` into `__method_Point_area(p)`.
	Methods map[string]string
}

// Check type-checks the program. It returns an aggregated error if any
// problems were found.
func Check(prog *ast.Program) (*Info, error) {
	c := &checker{
		info: &Info{
			VarTypes: map[*ast.Var]ast.Type{},
			Locals:   map[*ast.FuncDecl][]*ast.Var{},
			FuncSigs: map[string]*ast.FuncType{},
			Structs:  map[string]*ast.StructDecl{},
			Enums:    map[string]*ast.EnumDecl{},
			Methods:  map[string]string{},
		},
		variantOf: map[string]variantRef{},
	}

	// Register every struct declaration up front so that types
	// referenced by name (`function f(p: Point)`) resolve when we
	// check function signatures below.
	for _, sd := range prog.Structs {
		if _, dup := c.info.Structs[sd.Name]; dup {
			c.errf(sd.P, "struct %q redeclared", sd.Name)
			continue
		}
		seen := map[string]bool{}
		for _, f := range sd.Fields {
			if seen[f.Name] {
				c.errf(sd.P, "duplicate field %q in struct %s", f.Name, sd.Name)
			}
			seen[f.Name] = true
		}
		c.info.Structs[sd.Name] = sd
	}

	// Register every enum declaration. Variant names are recorded
	// in variantOf so an unqualified `Some(x)` or `Red` can be
	// rewritten into a typed *EnumLit during expression checking.
	// A variant name shared across two enums is ambiguous; we
	// drop the entry so subsequent uses surface a clear error.
	for _, ed := range prog.Enums {
		if _, dup := c.info.Enums[ed.Name]; dup {
			c.errf(ed.P, "enum %q redeclared", ed.Name)
			continue
		}
		c.info.Enums[ed.Name] = ed
		seen := map[string]bool{}
		for i := range ed.Variants {
			v := &ed.Variants[i]
			if seen[v.Name] {
				c.errf(v.P, "duplicate variant %q in enum %s", v.Name, ed.Name)
				continue
			}
			seen[v.Name] = true
			if existing, dup := c.variantOf[v.Name]; dup {
				c.errf(v.P, "variant %q is declared in both %s and %s — qualify references with the enum name (not yet supported, rename one)",
					v.Name, existing.enumName, ed.Name)
				delete(c.variantOf, v.Name)
				continue
			}
			c.variantOf[v.Name] = variantRef{
				enumName: ed.Name,
				index:    i,
				payloads: v.Payloads,
			}
		}
	}

	// Now that the enum set is known, walk every type position in
	// the program and rewrite StructType{Name: X} → EnumType when
	// X resolves to an enum. The parser doesn't know which named
	// types are structs vs. enums; we lazily disambiguate here.
	c.resolveTypeNames(prog)

	// Pre-declare built-ins so user code can call them.
	c.info.FuncSigs["putchar"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.VoidType{},
	}
	// print(s: string): void — appends a newline (lowers to libc puts).
	c.info.FuncSigs["print"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.VoidType{},
	}
	// write(s: string): void — stdout without a trailing newline.
	// Use this when you want to format your own output (status
	// lines, prompts, custom delimiters) instead of one line per
	// call. Pairs with `print` the way Go's `fmt.Print` /
	// `fmt.Println` do.
	c.info.FuncSigs["write"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.VoidType{},
	}
	// eprint(s: string): void — `print` shape but routed to stderr.
	// Useful for error / diagnostic output that shouldn't get
	// mixed in with stdout when the program is being piped to
	// another tool.
	c.info.FuncSigs["eprint"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.VoidType{},
	}
	// args(): string[] — returns the program's command-line argv as a
	// length-prefixed string array. The first element is conventionally
	// the program / module path (matching argv[0] in C and os.Args[0]
	// in Go). Building the array is one-shot and cached: the first
	// `args()` call materialises it from libc / WASI; subsequent calls
	// hand back the same pointer.
	c.info.FuncSigs["args"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.ArrayType{Elem: ast.StringType{}},
	}
	// read_line(): string — reads one line from stdin (terminated by
	// `\n`, which is preserved in the returned string). Returns an
	// empty string at end-of-file. A real empty line returns "\n",
	// so callers can disambiguate EOF from a blank line by length.
	c.info.FuncSigs["read_line"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.StringType{},
	}
	// env(name: string): string — reads an environment variable by
	// name. Returns an empty string when the variable isn't set.
	// (We'll add a richer Option-style API once the language has
	// the type machinery for it; the empty-string sentinel is the
	// MVP.)
	c.info.FuncSigs["env"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.StringType{},
	}
	// exit(code: number): void — exits the process immediately with
	// the given status code. Useful for `eprint(msg); exit(2)`-style
	// error paths; the success path can just `return` from main.
	c.info.FuncSigs["exit"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.VoidType{},
	}

	// First pass: gather all top-level signatures so functions can call
	// each other in any order. Methods are hoisted to mangled
	// top-level names (`__method_<Type>_<Name>`) with the receiver
	// prepended to the parameter list, so codegen never has to know
	// about methods.
	for _, fn := range prog.Funcs {
		if fn.Receiver != nil {
			st, ok := fn.Receiver.Type.(ast.StructType)
			if !ok {
				c.errf(fn.P, "method receiver type must be a struct, got %s", fn.Receiver.Type)
				continue
			}
			if _, ok := c.info.Structs[st.Name]; !ok {
				c.errf(fn.P, "method receiver references unknown struct %q", st.Name)
				continue
			}
			methodKey := st.Name + "." + fn.Name
			if _, dup := c.info.Methods[methodKey]; dup {
				c.errf(fn.P, "method %q on struct %s redeclared", fn.Name, st.Name)
				continue
			}
			mangled := "__method_" + st.Name + "_" + fn.Name
			// Rewrite the FuncDecl so codegen sees a regular
			// top-level function with the receiver as its first
			// parameter.
			fn.Name = mangled
			fn.Params = append([]ast.Param{*fn.Receiver}, fn.Params...)
			fn.Receiver = nil
			c.info.Methods[methodKey] = mangled
		}
		if _, dup := c.info.FuncSigs[fn.Name]; dup {
			c.errf(fn.P, "function %q redeclared", fn.Name)
			continue
		}
		params := make([]ast.Type, len(fn.Params))
		for i, p := range fn.Params {
			params[i] = p.Type
		}
		c.info.FuncSigs[fn.Name] = &ast.FuncType{Params: params, Result: fn.ReturnType}
	}

	// Second pass: check bodies.
	for _, fn := range prog.Funcs {
		c.checkFunction(fn)
	}

	if len(c.errors) > 0 {
		return c.info, diag.Errors(c.errors)
	}
	return c.info, nil
}

type checker struct {
	info        *Info
	errors      []error
	current     *ast.FuncDecl
	loopDepth   int
	switchDepth int

	// variantOf maps a variant's bare name (`Some`, `Err`) to the
	// enum that owns it plus its index. Built during the enum
	// registration pass; ambiguous names (same variant in two
	// enums) generate an error and the entry is left as zero-value
	// so subsequent uses report cleanly.
	variantOf map[string]variantRef

	// Closure-capture plumbing. While checking a local function body,
	// captureSink records each outer-scope name read by the body as
	// a capture; captureOuter is the scope of the immediately
	// enclosing function so we can look those names up. Both are nil
	// outside a local function.
	captureSink  func(name string, t ast.Type)
	captureOuter *scope
}

// resolveTypeNames walks every named-type position the parser may
// have stamped with `StructType{Name: X}` and rewrites it to
// `EnumType{Name: X}` when X turns out to be an enum. The parser
// can't distinguish structs from enums by name alone, so this
// pass runs after the enum decls have been collected. Function
// signatures, parameters, var decls, struct fields, array
// element types, and function-type fields are all visited.
//
// Inside an enum decl with type parameters, payload type
// references that match a parameter name (`T`, `U`) get
// rewritten to ParamType instead of StructType / EnumType. The
// parser can't tell them apart at decl time — both look like
// bare identifiers — so we disambiguate here against the enum's
// declared TypeParams set.
func (c *checker) resolveTypeNames(prog *ast.Program) {
	for _, fn := range prog.Funcs {
		if fn.Receiver != nil {
			c.resolveType(&fn.Receiver.Type, nil)
		}
		for i := range fn.Params {
			c.resolveType(&fn.Params[i].Type, nil)
		}
		c.resolveType(&fn.ReturnType, nil)
		c.resolveTypesInBlock(fn.Body)
	}
	for _, sd := range prog.Structs {
		for i := range sd.Fields {
			c.resolveType(&sd.Fields[i].Type, nil)
		}
	}
	for _, ed := range prog.Enums {
		var params map[string]bool
		if len(ed.TypeParams) > 0 {
			params = make(map[string]bool, len(ed.TypeParams))
			for _, n := range ed.TypeParams {
				params[n] = true
			}
		}
		for i := range ed.Variants {
			for j := range ed.Variants[i].Payloads {
				c.resolveType(&ed.Variants[i].Payloads[j], params)
			}
		}
	}
}

func (c *checker) resolveTypesInBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		switch x := st.(type) {
		case *ast.Block:
			c.resolveTypesInBlock(x)
		case *ast.If:
			c.resolveTypesInBlock(asBlock(x.Then))
			c.resolveTypesInBlock(asBlock(x.Else))
		case *ast.While:
			c.resolveTypesInBlock(asBlock(x.Body))
		case *ast.For:
			c.resolveTypesInBlock(asBlock(x.Body))
		case *ast.Var:
			c.resolveType(&x.Type, nil)
		case *ast.Switch:
			for _, k := range x.Cases {
				c.resolveTypesInBlock(k.Body)
			}
			c.resolveTypesInBlock(x.Default)
		case *ast.Match:
			for _, arm := range x.Arms {
				c.resolveTypesInBlock(arm.Body)
			}
		case *ast.FuncDecl:
			for i := range x.Params {
				c.resolveType(&x.Params[i].Type, nil)
			}
			c.resolveType(&x.ReturnType, nil)
			c.resolveTypesInBlock(x.Body)
		}
	}
}

// asBlock turns a Stmt into a *Block where possible — used to
// reuse resolveTypesInBlock for the if/while/for body slots,
// which are typed as Stmt but in practice always Block. Returns
// nil otherwise so the caller skips.
func asBlock(s ast.Stmt) *ast.Block {
	if b, ok := s.(*ast.Block); ok {
		return b
	}
	return nil
}

// resolveType rewrites a single Type slot in place. Handles
// nominal references (StructType promoted to EnumType when
// appropriate, or ParamType when the name matches an enclosing
// enum's type parameter) plus recurses into composite types.
//
// `params` carries the type-parameter names visible at this
// type position. It's nil outside of enum-body contexts. When
// the name is in `params`, we always rewrite to ParamType —
// the parameter wins over a same-named enum or struct.
func (c *checker) resolveType(slot *ast.Type, params map[string]bool) {
	if slot == nil || *slot == nil {
		return
	}
	switch t := (*slot).(type) {
	case ast.StructType:
		if params[t.Name] {
			*slot = ast.ParamType{Name: t.Name}
			return
		}
		if _, isEnum := c.info.Enums[t.Name]; isEnum {
			*slot = ast.EnumType{Name: t.Name}
		}
	case ast.EnumType:
		// `Foo[A, B]` — recurse into args. Params can shadow
		// individual arg names too (e.g. `Option[T]` inside an
		// enum body where T is a parameter). We also enforce the
		// arity here so a wrong-arity instantiation fails before
		// it becomes a "no Args" enum at the assignment site.
		args := make([]ast.Type, len(t.Args))
		copy(args, t.Args)
		for i := range args {
			c.resolveType(&args[i], params)
		}
		if ed, ok := c.info.Enums[t.Name]; ok {
			if len(ed.TypeParams) != len(args) {
				c.errf(ed.P, "enum %s has %d type parameter(s), %d supplied",
					t.Name, len(ed.TypeParams), len(args))
			}
		}
		*slot = ast.EnumType{Name: t.Name, Args: args}
	case ast.ArrayType:
		elem := t.Elem
		c.resolveType(&elem, params)
		*slot = ast.ArrayType{Elem: elem}
	case *ast.FuncType:
		for i := range t.Params {
			c.resolveType(&t.Params[i], params)
		}
		c.resolveType(&t.Result, params)
	}
}

// variantRef is the resolution target for an unqualified variant
// name. The checker uses it to rewrite `Circle(3.0)` /
// `Red` into a typed *EnumLit.
type variantRef struct {
	enumName string
	index    int
	payloads []ast.Type
}

// substituteType returns t with every ParamType reference
// replaced by the type bound to that parameter in `sub`. Unbound
// parameters fall through unchanged so the caller can detect
// "couldn't fully resolve" cases. Recurses into composite types
// (arrays, function types, generic enum args) so a payload like
// `Option[T]` resolves to `Option[number]` when T=number.
func substituteType(t ast.Type, sub map[string]ast.Type) ast.Type {
	if t == nil {
		return nil
	}
	switch x := t.(type) {
	case ast.ParamType:
		if v, ok := sub[x.Name]; ok {
			return v
		}
		return x
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substituteType(x.Args[i], sub)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.ArrayType:
		return ast.ArrayType{Elem: substituteType(x.Elem, sub)}
	case *ast.FuncType:
		out := &ast.FuncType{Result: substituteType(x.Result, sub)}
		for _, p := range x.Params {
			out.Params = append(out.Params, substituteType(p, sub))
		}
		return out
	}
	return t
}

// unifyType records the substitutions that would make `expected`
// (a type containing ParamType references) equal to `actual` (a
// concrete type). Updates `sub` in place and returns false on
// conflict. Concrete-vs-concrete still goes through ast.Equal,
// so existing strict-checking behaviour is preserved for
// monomorphic enums.
func (c *checker) unifyType(expected, actual ast.Type, sub map[string]ast.Type) bool {
	if expected == nil || actual == nil {
		return false
	}
	if p, ok := expected.(ast.ParamType); ok {
		if existing, bound := sub[p.Name]; bound {
			return ast.Equal(existing, actual)
		}
		sub[p.Name] = actual
		return true
	}
	// Generic enum positions: unify pairwise.
	if e, ok := expected.(ast.EnumType); ok {
		a, ok := actual.(ast.EnumType)
		if !ok || a.Name != e.Name || len(a.Args) != len(e.Args) {
			return false
		}
		for i := range e.Args {
			if !c.unifyType(e.Args[i], a.Args[i], sub) {
				return false
			}
		}
		return true
	}
	// Arrays + function types decompose the same way.
	if e, ok := expected.(ast.ArrayType); ok {
		a, ok := actual.(ast.ArrayType)
		return ok && c.unifyType(e.Elem, a.Elem, sub)
	}
	if e, ok := expected.(*ast.FuncType); ok {
		a, ok := actual.(*ast.FuncType)
		if !ok || len(e.Params) != len(a.Params) {
			return false
		}
		for i := range e.Params {
			if !c.unifyType(e.Params[i], a.Params[i], sub) {
				return false
			}
		}
		return c.unifyType(e.Result, a.Result, sub)
	}
	return ast.Equal(expected, actual)
}

// assignable reports whether a value of type `src` can flow into
// a slot expecting `dst`. It's strictly equal in most cases;
// the one relaxation is for payload-less variants on generic
// enums where the construction site can't infer the type
// arguments. `None` produces `EnumType{"Option", nil}` which
// flows into `Option[number]` here without complaint.
func assignable(dst, src ast.Type) bool {
	if ast.Equal(dst, src) {
		return true
	}
	d, dok := dst.(ast.EnumType)
	s, sok := src.(ast.EnumType)
	if dok && sok && d.Name == s.Name && len(s.Args) == 0 && len(d.Args) > 0 {
		return true
	}
	return false
}

func (c *checker) errf(pos ast.Position, format string, args ...any) {
	c.errors = append(c.errors, &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// errIdent reports an unresolved-name error and tries to attach a
// "did you mean foo?" hint by scanning every name visible in scope
// (locals, params, top-level functions). The error span covers the
// whole identifier so the squiggle underlines the misspelt name.
func (c *checker) errIdent(n *ast.Ident, s *scope, format string, args ...any) {
	cands := c.collectNames(s)
	suggestion := diag.Suggest(n.Name, cands)
	e := &Error{
		Pos:  n.P,
		Span: len(n.Name),
		Msg:  fmt.Sprintf(format, args...),
	}
	if suggestion != "" {
		e.Note = fmt.Sprintf("did you mean %q?", suggestion)
	}
	c.errors = append(c.errors, e)
}

// collectNames flattens every name reachable from s, plus all top-level
// function names, into a single slice for diag.Suggest to scan.
func (c *checker) collectNames(s *scope) []string {
	seen := map[string]bool{}
	var out []string
	for cur := s; cur != nil; cur = cur.parent {
		for name := range cur.names {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	for name := range c.info.FuncSigs {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// isUserFuncOrLocal reports whether name shadows the implicit `len`
// builtin via a user-declared function or in-scope variable. Callers
// use it to decide whether to apply the builtin's special typing
// rules — a user that explicitly defines `len` wins.
func (c *checker) isUserFuncOrLocal(name string, s *scope) bool {
	if _, ok := s.lookup(name); ok {
		return true
	}
	if _, ok := c.info.FuncSigs[name]; ok {
		return true
	}
	return false
}

// scope is an environment of named bindings plus a pointer to its parent.
type scope struct {
	parent *scope
	names  map[string]ast.Type
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent, names: map[string]ast.Type{}}
}

func (s *scope) lookup(name string) (ast.Type, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if t, ok := cur.names[name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (c *checker) checkFunction(fn *ast.FuncDecl) {
	c.current = fn
	defer func() { c.current = nil }()

	root := newScope(nil)
	for _, p := range fn.Params {
		if _, dup := root.names[p.Name]; dup {
			c.errf(fn.P, "duplicate parameter %q", p.Name)
		}
		root.names[p.Name] = p.Type
	}
	c.checkBlock(fn.Body, root)
}

func (c *checker) checkBlock(b *ast.Block, parent *scope) {
	s := newScope(parent)
	for _, st := range b.Stmts {
		c.checkStmt(st, s)
	}
}

func (c *checker) checkStmt(st ast.Stmt, s *scope) {
	switch n := st.(type) {
	case *ast.Block:
		c.checkBlock(n, s)
	case *ast.If:
		t := c.checkExpr(n.Cond, s)
		if t != nil && !ast.Equal(t, ast.BoolType{}) {
			c.errf(n.Cond.Pos(), "if condition must be boolean, got %s", t)
		}
		c.checkStmt(n.Then, s)
		if n.Else != nil {
			c.checkStmt(n.Else, s)
		}
	case *ast.While:
		t := c.checkExpr(n.Cond, s)
		if t != nil && !ast.Equal(t, ast.BoolType{}) {
			c.errf(n.Cond.Pos(), "while condition must be boolean, got %s", t)
		}
		c.loopDepth++
		c.checkStmt(n.Body, s)
		c.loopDepth--
	case *ast.For:
		// Init runs in a new scope so a `for (var i = 0; ...)` doesn't
		// leak `i` to the surrounding block.
		inner := newScope(s)
		if n.Init != nil {
			c.checkStmt(n.Init, inner)
		}
		ct := c.checkExpr(n.Cond, inner)
		if ct != nil && !ast.Equal(ct, ast.BoolType{}) {
			c.errf(n.Cond.Pos(), "for condition must be boolean, got %s", ct)
		}
		c.loopDepth++
		c.checkStmt(n.Body, inner)
		if n.Step != nil {
			c.checkStmt(n.Step, inner)
		}
		c.loopDepth--
	case *ast.Break:
		// `break` is legal inside a `for`/`while` (exits the loop)
		// or inside a `switch` case (exits the switch).
		if c.loopDepth == 0 && c.switchDepth == 0 {
			c.errf(n.P, "break outside of a loop or switch")
		}
	case *ast.Continue:
		if c.loopDepth == 0 {
			c.errf(n.P, "continue outside of a loop")
		}
	case *ast.Return:
		want := c.current.ReturnType
		if n.Value == nil {
			if !ast.Equal(want, ast.VoidType{}) {
				c.errf(n.P, "return without value in function returning %s", want)
			}
			return
		}
		got := c.checkExpr(n.Value, s)
		if got != nil && !assignable(want, got) {
			c.errf(n.P, "return type mismatch: function returns %s but expression is %s", want, got)
		}
	case *ast.Var:
		if _, dup := s.names[n.Name]; dup {
			c.errf(n.P, "variable %q already declared in this scope", n.Name)
		}
		got := c.checkExpr(n.Init, s)
		if n.Type == nil {
			if got == nil {
				return
			}
			n.Type = got
		} else if got != nil && !assignable(n.Type, got) {
			c.errf(n.P, "cannot assign %s to variable of type %s", got, n.Type)
		}
		s.names[n.Name] = n.Type
		c.info.VarTypes[n] = n.Type
		c.info.Locals[c.current] = append(c.info.Locals[c.current], n)
	case *ast.ExprStmt:
		c.checkExpr(n.Expr, s)
	case *ast.Switch:
		tagT := c.checkExpr(n.Tag, s)
		// Floats compare with NaN edge cases that switch's "exact
		// match" semantics aren't well-defined for. Reject them up
		// front rather than letting WASM's f32.eq surprise us.
		if tagT != nil && ast.Equal(tagT, ast.FloatType{}) {
			c.errf(n.Tag.Pos(), "switch on float values is not supported")
		}
		// `break` inside case bodies should leave the switch but not
		// abort an enclosing loop. `continue` falls straight through
		// to the enclosing real loop and is invalid otherwise — that's
		// why we bump switchDepth (a break-only counter), not loopDepth.
		c.switchDepth++
		for _, k := range n.Cases {
			for _, v := range k.Values {
				vt := c.checkExpr(v, s)
				if tagT != nil && vt != nil && !ast.Equal(vt, tagT) {
					c.errf(v.Pos(), "case value type %s, expected %s", vt, tagT)
				}
			}
			c.checkBlock(k.Body, s)
		}
		if n.Default != nil {
			c.checkBlock(n.Default, s)
		}
		c.switchDepth--
	case *ast.Match:
		c.checkMatch(n, s)
	case *ast.FuncDecl:
		c.checkLocalFunc(n, s)
	}
}

// checkMatch type-checks a `match` statement. The scrutinee must
// be an enum value; each arm's pattern variant must belong to
// that enum and supply the right number of binding names; the
// arm list must cover every variant of the enum (or end in a
// wildcard). Bindings are typed against the matching variant's
// payload list and bound in a fresh per-arm scope.
func (c *checker) checkMatch(n *ast.Match, s *scope) {
	tagT := c.checkExpr(n.Tag, s)
	if tagT == nil {
		return
	}
	et, ok := tagT.(ast.EnumType)
	if !ok {
		c.errf(n.Tag.Pos(), "match scrutinee must be an enum value, got %s", tagT)
		return
	}
	ed, ok := c.info.Enums[et.Name]
	if !ok {
		c.errf(n.Tag.Pos(), "unknown enum %q", et.Name)
		return
	}
	// For generic enums, build a substitution map from the
	// scrutinee's concrete type arguments. `match (o: Option[number])`
	// gives us T=number, so the arm `Some(v)` types `v` as
	// `number` rather than the unresolved `T`.
	sub := map[string]ast.Type{}
	if len(ed.TypeParams) == len(et.Args) {
		for i, p := range ed.TypeParams {
			sub[p] = et.Args[i]
		}
	}
	covered := map[string]bool{}
	sawWildcard := false
	for i, arm := range n.Arms {
		if arm.IsWildcard {
			if i != len(n.Arms)-1 {
				c.errf(arm.P, "wildcard `_` arm must be last in the match")
			}
			sawWildcard = true
			c.checkBlock(arm.Body, s)
			continue
		}
		// Find the variant on this enum.
		varIdx := -1
		var variant *ast.EnumVariant
		for j := range ed.Variants {
			if ed.Variants[j].Name == arm.VariantName {
				varIdx = j
				variant = &ed.Variants[j]
				break
			}
		}
		if varIdx < 0 {
			c.errf(arm.P, "variant %q is not part of enum %s", arm.VariantName, ed.Name)
			c.checkBlock(arm.Body, s)
			continue
		}
		if covered[arm.VariantName] {
			c.errf(arm.P, "variant %q already covered earlier in this match", arm.VariantName)
		}
		covered[arm.VariantName] = true
		if len(arm.Bindings) != len(variant.Payloads) {
			c.errf(arm.P, "variant %s has %d payload(s), got %d binding(s)",
				arm.VariantName, len(variant.Payloads), len(arm.Bindings))
		}
		// Bind names in a fresh scope so they don't leak into
		// sibling arms. Payload types get the type-parameter
		// substitution applied so `Some(v)` on `Option[number]`
		// types `v` as `number`, not the abstract `T`.
		armScope := newScope(s)
		arm.BindingTypes = make([]ast.Type, len(arm.Bindings))
		for k, name := range arm.Bindings {
			var bt ast.Type
			if k < len(variant.Payloads) {
				bt = substituteType(variant.Payloads[k], sub)
			}
			arm.BindingTypes[k] = bt
			armScope.names[name] = bt
		}
		c.checkBlock(arm.Body, armScope)
	}
	if !sawWildcard {
		for _, v := range ed.Variants {
			if !covered[v.Name] {
				c.errf(n.P, "match is not exhaustive — variant %s of enum %s is not covered (add an arm or use `_`)",
					v.Name, ed.Name)
			}
		}
	}
}

// checkLocalFunc type-checks a nested function and records its
// captured outer-scope variables. The local name is bound in the
// surrounding scope so subsequent calls (and recursion through the
// inner name) work; the body checks under a fresh root scope with
// its own params, plus a capture-sink that registers any outer-scope
// name the body reads.
func (c *checker) checkLocalFunc(fn *ast.FuncDecl, outer *scope) {
	// Bind the function's name in the outer scope so subsequent code
	// can call it.
	sig := &ast.FuncType{Result: fn.ReturnType}
	for _, p := range fn.Params {
		sig.Params = append(sig.Params, p.Type)
	}
	outer.names[fn.Name] = sig

	// Body scope: fresh root with the function's own params.
	root := newScope(nil)
	for _, p := range fn.Params {
		if _, dup := root.names[p.Name]; dup {
			c.errf(fn.P, "duplicate parameter %q", p.Name)
		}
		root.names[p.Name] = p.Type
	}

	captured := map[string]ast.Type{}
	var captureOrder []string

	prev := c.current
	prevSink := c.captureSink
	prevOuter := c.captureOuter
	prevLoop := c.loopDepth
	prevSwitch := c.switchDepth
	c.current = fn
	c.loopDepth = 0
	c.switchDepth = 0
	c.captureSink = func(name string, t ast.Type) {
		if _, ok := captured[name]; ok {
			return
		}
		// Recursive self-reference shouldn't capture: the inner
		// function's name is bound in the outer scope above so the
		// lookup falls through here, but we don't want to treat it
		// as a capture.
		if name == fn.Name {
			return
		}
		// Only allow capturing scalar types in this PR. References
		// (string/array/struct/function) would need indirection
		// through the env that we haven't designed yet.
		switch t.(type) {
		case ast.NumberType, ast.BoolType, ast.FloatType:
			captured[name] = t
			captureOrder = append(captureOrder, name)
		default:
			c.errf(fn.P, "captured variable %q has unsupported type %s (only number, boolean, float can be captured)", name, t)
		}
	}
	c.captureOuter = outer
	defer func() {
		c.current = prev
		c.captureSink = prevSink
		c.captureOuter = prevOuter
		c.loopDepth = prevLoop
		c.switchDepth = prevSwitch
	}()

	c.checkBlock(fn.Body, root)

	for _, name := range captureOrder {
		fn.Captures = append(fn.Captures, ast.Param{Name: name, Type: captured[name]})
	}
	// Track the local function's signature so call sites can look it
	// up by name. Codegen's hoisting pass will rename it later.
	c.info.FuncSigs[fn.Name] = sig
}

func (c *checker) checkExpr(e ast.Expr, s *scope) ast.Type {
	switch n := e.(type) {
	case *ast.NumberLit:
		return ast.NumberType{}
	case *ast.BoolLit:
		return ast.BoolType{}
	case *ast.StringLit:
		return ast.StringType{}
	case *ast.FloatLit:
		return ast.FloatType{}
	case *ast.Ident:
		if t, ok := s.lookup(n.Name); ok {
			return t
		}
		if sig, ok := c.info.FuncSigs[n.Name]; ok {
			return sig
		}
		// A bare name might be a payload-less enum variant.
		// (Variants with payloads are constructed via Call and
		// rejected here so the user gets a clearer error.)
		if vr, ok := c.variantOf[n.Name]; ok {
			if len(vr.payloads) > 0 {
				c.errf(n.P, "variant %s expects %d payload argument(s); call it as %s(...)",
					n.Name, len(vr.payloads), n.Name)
				return nil
			}
			return ast.EnumType{Name: vr.enumName}
		}
		// Inside a local function: a name not found in the local
		// scope might resolve in the enclosing function's scope.
		// Record it as a capture and return its outer type.
		if c.captureOuter != nil {
			if t, ok := c.captureOuter.lookup(n.Name); ok {
				if c.captureSink != nil {
					c.captureSink(n.Name, t)
				}
				return t
			}
		}
		c.errIdent(n, s, "undefined identifier %q", n.Name)
		return nil
	case *ast.ArrayLit:
		if len(n.Elems) == 0 {
			c.errf(n.P, "empty array literal needs a type annotation")
			return nil
		}
		elemT := c.checkExpr(n.Elems[0], s)
		for _, el := range n.Elems[1:] {
			t := c.checkExpr(el, s)
			if t != nil && elemT != nil && !ast.Equal(t, elemT) {
				c.errf(el.Pos(), "array element type %s, expected %s", t, elemT)
			}
		}
		return ast.ArrayType{Elem: elemT}
	case *ast.Index:
		at := c.checkExpr(n.Array, s)
		it := c.checkExpr(n.Idx, s)
		if it != nil && !ast.Equal(it, ast.NumberType{}) {
			c.errf(n.Idx.Pos(), "index must be number, got %s", it)
		}
		if arr, ok := at.(ast.ArrayType); ok {
			return arr.Elem
		}
		// `s[i]` on a string returns the byte at i as a number.
		if _, ok := at.(ast.StringType); ok {
			n.IsString = true
			return ast.NumberType{}
		}
		if at != nil {
			c.errf(n.P, "indexing non-array value of type %s", at)
		}
		return nil
	case *ast.Call:
		// Variant constructor: `Some(x)` / `Square(2.0, 3.0)`.
		// Resolved purely by name so we can type the result as
		// the owning enum and check argument count + payload
		// types. Shadowing by a function or local is intentional —
		// the user can re-bind the name and the variant becomes
		// inaccessible until the shadow leaves scope.
		//
		// For generic enums (`Option[T]`, `Result[T, E]`), the
		// type arguments get inferred from the actual payload arg
		// types — `Some(42)` → `Option[number]`. Inference only
		// works when there's at least one type-determining
		// payload arg; payload-less variants on generic enums
		// (like `None`) yield an EnumType with empty Args, which
		// the assignment relaxation in `assignable` lets flow
		// into a concretely-typed slot at the var / return site.
		if id, ok := n.Callee.(*ast.Ident); ok {
			if vr, ok := c.variantOf[id.Name]; ok && !c.isUserFuncOrLocal(id.Name, s) {
				if len(n.Args) != len(vr.payloads) {
					c.errf(n.P, "variant %s expects %d argument(s), got %d",
						id.Name, len(vr.payloads), len(n.Args))
				}
				ed := c.info.Enums[vr.enumName]
				sub := map[string]ast.Type{}
				for i, a := range n.Args {
					at := c.checkExpr(a, s)
					if i >= len(vr.payloads) || at == nil {
						continue
					}
					if !c.unifyType(vr.payloads[i], at, sub) {
						c.errf(a.Pos(), "variant %s payload %d type %s, expected %s",
							id.Name, i, at, substituteType(vr.payloads[i], sub))
					}
				}
				if ed != nil && len(ed.TypeParams) > 0 {
					args := make([]ast.Type, len(ed.TypeParams))
					complete := true
					for i, p := range ed.TypeParams {
						if v, ok := sub[p]; ok {
							args[i] = v
						} else {
							complete = false
						}
					}
					if !complete {
						// Couldn't fill in every parameter from
						// the args alone (typical for a
						// payload-less variant). Leave Args nil
						// so `assignable` flows the type into
						// whatever the surrounding context
						// expects.
						return ast.EnumType{Name: vr.enumName}
					}
					return ast.EnumType{Name: vr.enumName, Args: args}
				}
				return ast.EnumType{Name: vr.enumName}
			}
		}
		// `len(x)` is a generic builtin: it accepts any string or
		// array and returns a number. We type-check it here rather
		// than in FuncSigs because no monomorphic FuncType expresses
		// the union.
		if id, ok := n.Callee.(*ast.Ident); ok && id.Name == "len" && !c.isUserFuncOrLocal(id.Name, s) {
			if len(n.Args) != 1 {
				c.errf(n.P, "len expects 1 argument, got %d", len(n.Args))
				return ast.NumberType{}
			}
			at := c.checkExpr(n.Args[0], s)
			switch at.(type) {
			case ast.StringType, ast.ArrayType:
				// fine
			default:
				if at != nil {
					c.errf(n.Args[0].Pos(), "len: expected string or array, got %s", at)
				}
			}
			return ast.NumberType{}
		}
		// Method call dispatch: `target.method(args)` where target is a
		// struct value and the struct has a method of that name. We
		// rewrite the Call node in place to `mangledName(target, args)`
		// so the rest of the pipeline (codegen, IR) only ever sees a
		// regular function call.
		if fa, ok := n.Callee.(*ast.FieldAccess); ok {
			tt := c.checkExpr(fa.Target, s)
			if st, ok := tt.(ast.StructType); ok {
				key := st.Name + "." + fa.Field
				if mangled, ok := c.info.Methods[key]; ok {
					n.Callee = &ast.Ident{P: fa.P, Name: mangled}
					n.Args = append([]ast.Expr{fa.Target}, n.Args...)
				}
			}
		}
		callee := c.checkExpr(n.Callee, s)
		ft, ok := callee.(*ast.FuncType)
		if !ok {
			if callee != nil {
				c.errf(n.P, "calling non-function value of type %s", callee)
			}
			return nil
		}
		if len(n.Args) != len(ft.Params) {
			c.errf(n.P, "function expects %d arguments, got %d", len(ft.Params), len(n.Args))
		}
		for i, a := range n.Args {
			at := c.checkExpr(a, s)
			if i < len(ft.Params) && at != nil && !assignable(ft.Params[i], at) {
				c.errf(a.Pos(), "argument %d: expected %s, got %s", i+1, ft.Params[i], at)
			}
		}
		return ft.Result
	case *ast.Binary:
		lt := c.checkExpr(n.Left, s)
		rt := c.checkExpr(n.Right, s)
		switch n.Op {
		case "+":
			// Special case: string + string is concatenation.
			if _, lOk := lt.(ast.StringType); lOk {
				if _, rOk := rt.(ast.StringType); rOk {
					n.IsStringConcat = true
					return ast.StringType{}
				}
			}
			fallthrough
		case "-", "*", "/":
			// Same-type number+number or float+float arithmetic.
			if isFloat(lt) || isFloat(rt) {
				c.requireFloat(n.P, lt, n.Op)
				c.requireFloat(n.P, rt, n.Op)
				n.IsFloat = true
				return ast.FloatType{}
			}
			c.requireNumber(n.P, lt, n.Op)
			c.requireNumber(n.P, rt, n.Op)
			return ast.NumberType{}
		case "%", "&", "|", "^", "<<", ">>":
			c.requireNumber(n.P, lt, n.Op)
			c.requireNumber(n.P, rt, n.Op)
			return ast.NumberType{}
		case "<", ">", "<=", ">=":
			if isFloat(lt) || isFloat(rt) {
				c.requireFloat(n.P, lt, n.Op)
				c.requireFloat(n.P, rt, n.Op)
				n.IsFloat = true
				return ast.BoolType{}
			}
			c.requireNumber(n.P, lt, n.Op)
			c.requireNumber(n.P, rt, n.Op)
			return ast.BoolType{}
		case "==", "!=":
			if lt != nil && rt != nil && !ast.Equal(lt, rt) {
				c.errf(n.P, "cannot compare %s and %s", lt, rt)
			}
			// String-vs-string equality compares contents; flag so
			// codegen lowers to a runtime call rather than i32.eq.
			if _, ok := lt.(ast.StringType); ok {
				if _, ok := rt.(ast.StringType); ok {
					n.IsStringCmp = true
				}
			}
			return ast.BoolType{}
		case "&&", "||":
			c.requireBool(n.P, lt, n.Op)
			c.requireBool(n.P, rt, n.Op)
			return ast.BoolType{}
		}
		c.errf(n.P, "unknown binary operator %q", n.Op)
		return nil
	case *ast.Unary:
		t := c.checkExpr(n.Operand, s)
		switch n.Op {
		case "-":
			if isFloat(t) {
				n.IsFloat = true
				return ast.FloatType{}
			}
			c.requireNumber(n.P, t, n.Op)
			return ast.NumberType{}
		case "!":
			c.requireBool(n.P, t, n.Op)
			return ast.BoolType{}
		}
		return nil
	case *ast.Assign:
		lt := c.checkExpr(n.Target, s)
		rt := c.checkExpr(n.Value, s)
		if lt != nil && rt != nil && !ast.Equal(lt, rt) {
			c.errf(n.P, "cannot assign %s to %s", rt, lt)
		}
		// Restrict assignment targets the same way `=` does for
		// arrays: only Ident, Index and FieldAccess are addressable.
		if _, ok := n.Target.(*ast.FieldAccess); !ok {
			if _, ok := n.Target.(*ast.Ident); !ok {
				if _, ok := n.Target.(*ast.Index); !ok {
					// Already errored elsewhere when the parser built
					// the Assign — nothing to add here.
				}
			}
		}
		return lt
	case *ast.Ternary:
		ct := c.checkExpr(n.Cond, s)
		if ct != nil && !ast.Equal(ct, ast.BoolType{}) {
			c.errf(n.Cond.Pos(), "ternary condition must be boolean, got %s", ct)
		}
		tt := c.checkExpr(n.Then, s)
		et := c.checkExpr(n.Else, s)
		if tt != nil && et != nil && !ast.Equal(tt, et) {
			c.errf(n.P, "ternary branches differ: %s vs %s", tt, et)
		}
		result := tt
		if result == nil {
			result = et
		}
		if isFloat(result) {
			n.IsFloat = true
		}
		return result
	case *ast.StructLit:
		sd, ok := c.info.Structs[n.TypeName]
		if !ok {
			c.errf(n.P, "unknown struct type %q", n.TypeName)
			return nil
		}
		// Each declared field must be initialised exactly once and
		// have the right type. Surplus / unknown fields are an error.
		seen := map[string]bool{}
		fieldT := map[string]ast.Type{}
		for _, f := range sd.Fields {
			fieldT[f.Name] = f.Type
		}
		for _, f := range n.Fields {
			if _, ok := fieldT[f.Name]; !ok {
				c.errf(n.P, "struct %s has no field %q", sd.Name, f.Name)
				continue
			}
			if seen[f.Name] {
				c.errf(n.P, "duplicate field %q in struct literal", f.Name)
			}
			seen[f.Name] = true
			vt := c.checkExpr(f.Value, s)
			if vt != nil && !ast.Equal(vt, fieldT[f.Name]) {
				c.errf(f.Value.Pos(), "field %q: expected %s, got %s", f.Name, fieldT[f.Name], vt)
			}
		}
		for _, f := range sd.Fields {
			if !seen[f.Name] {
				c.errf(n.P, "struct literal missing field %q", f.Name)
			}
		}
		return ast.StructType{Name: sd.Name}
	case *ast.FieldAccess:
		tt := c.checkExpr(n.Target, s)
		st, ok := tt.(ast.StructType)
		if !ok {
			if tt != nil {
				c.errf(n.P, "field access on non-struct value of type %s", tt)
			}
			return nil
		}
		sd := c.info.Structs[st.Name]
		if sd == nil {
			c.errf(n.P, "unknown struct type %q", st.Name)
			return nil
		}
		for _, f := range sd.Fields {
			if f.Name == n.Field {
				return f.Type
			}
		}
		c.errf(n.P, "struct %s has no field %q", st.Name, n.Field)
		return nil
	}
	return nil
}

func (c *checker) requireNumber(p ast.Position, t ast.Type, op string) {
	if t != nil && !ast.Equal(t, ast.NumberType{}) {
		c.errf(p, "operator %q requires number, got %s", op, t)
	}
}
func (c *checker) requireFloat(p ast.Position, t ast.Type, op string) {
	if t != nil && !ast.Equal(t, ast.FloatType{}) {
		c.errf(p, "operator %q requires float, got %s", op, t)
	}
}
func isFloat(t ast.Type) bool {
	_, ok := t.(ast.FloatType)
	return ok
}
func (c *checker) requireBool(p ast.Position, t ast.Type, op string) {
	if t != nil && !ast.Equal(t, ast.BoolType{}) {
		c.errf(p, "operator %q requires boolean, got %s", op, t)
	}
}
