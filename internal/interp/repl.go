package interp

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// REPL reads lines from in, evaluates each, and writes the result (or
// any error) to out. It returns when in reaches EOF.
//
// Each line is tried as, in order:
//
//  1. one or more top-level function declarations,
//  2. an expression — its value is printed,
//  3. a statement (var, assignment, if, while, for, …) — silent on
//     success.
//
// State persists across lines via a single Interp instance.
func REPL(in io.Reader, out io.Writer) error {
	i := New()
	i.Stdout = out
	s := bufio.NewScanner(in)
	fmt.Fprintln(out, "lang REPL — Ctrl+D to exit")
	for {
		fmt.Fprint(out, "> ")
		if !s.Scan() {
			fmt.Fprintln(out)
			return s.Err()
		}
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		if val, printIt, err := EvalLine(i, line); err != nil {
			fmt.Fprintln(out, "error:", err)
		} else if printIt {
			fmt.Fprintln(out, val.String())
		}
	}
}

// EvalLine runs a single REPL line. The bool says whether the caller
// should print val: true for the value of a final expression, false
// for declarations and statements with no observable result.
//
// Statements run directly against Interp.Global, so `var x = 7` at
// one prompt is visible at the next.
func EvalLine(i *Interp, line string) (Value, bool, error) {
	// Top-level function declaration(s).
	if parsedAsTopLevel(line) {
		prog, err := parser.Parse(line)
		if err != nil {
			return nil, false, err
		}
		for _, ed := range prog.Enums {
			i.RegisterEnum(ed)
		}
		for _, fn := range prog.Funcs {
			i.Register(fn)
		}
		return Void{}, false, nil
	}

	// Wrap the rest as a void function body so the existing parser
	// can validate it. Bare expressions get a `;` appended so the
	// expression-statement form is well-formed.
	src := strings.TrimSpace(line)
	if !strings.HasSuffix(src, ";") && !strings.HasSuffix(src, "}") {
		src += ";"
	}
	prog, err := parser.Parse("function __repl(): void { " + src + " }")
	if err != nil {
		return nil, false, err
	}
	fn := findFunc(prog, "__repl")
	if fn == nil {
		return nil, false, fmt.Errorf("internal: missing __repl wrapper")
	}
	stmts := fn.Body.Stmts
	for k, s := range stmts {
		// On the last statement, if it's an ExprStmt we want to
		// surface the value rather than discard it — that's what
		// makes `1 + 2` at the prompt print `3`.
		if k == len(stmts)-1 {
			if es, ok := s.(*ast.ExprStmt); ok {
				v, err := i.evalExpr(es.Expr, i.Global)
				if err != nil {
					return nil, false, err
				}
				if _, isVoid := v.(Void); isVoid {
					return Void{}, false, nil
				}
				return v, true, nil
			}
		}
		if _, err := i.execStmt(s, i.Global); err != nil {
			return nil, false, err
		}
	}
	return Void{}, false, nil
}

// parsedAsTopLevel reports whether the trimmed line starts with the
// `function` keyword.
func parsedAsTopLevel(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "function ") || strings.HasPrefix(t, "function\t")
}

func findFunc(prog *ast.Program, name string) *ast.FuncDecl {
	for _, fn := range prog.Funcs {
		if fn.Name == name {
			return fn
		}
	}
	return nil
}
