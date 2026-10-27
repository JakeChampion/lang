package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const printerAssignmentContract = `import "./printer";
import "./parser";
import "./lexer";
import "./ast";

function tagged(body: ast.Stmt[], origin: string): ast.Stmt {
  let form = match (parser.s_if(parser.e_bool(true), body, [])) {
    ast.StmtIf(value) => value,
    _ => { exit(90); }
  };
  return ast.StmtIf { ...form, origin: origin };
}

function format(body: ast.Stmt[]): string {
  let mod = parser.parse_module(lexer.tokenize("function main(): i32 { return 0; }"));
  let fd = parser.FuncDecl { ...mod.funcs[0], body: body };
  return printer.print_module(parser.Module { ...mod, funcs: [fd] });
}

function unchanged(body: ast.Stmt[]): boolean {
  return format([tagged(body, "chain_assign")]) == format([tagged(body, "block")]);
}

function main(): i32 {
  let value = parser.e_number("7", "", false);
  let valid = parser.s_assign("x", value);
  let ret = parser.s_return(value);
  let expr = ast.StmtExpr { value: value, line: 0 };
  let wrong_name = ast.StmtExpr { value: parser.e_call(parser.e_ident("call"), [value, value]), line: 0 };
  let wrong_arity = ast.StmtExpr { value: parser.e_call(parser.e_ident("__set_field"), [value]), line: 0 };
  if (!unchanged([])) { return 1; }
  if (!unchanged([ret])) { return 2; }
  if (!unchanged([expr])) { return 3; }
  if (!unchanged([valid, ret])) { return 4; }
  if (!unchanged([ret, valid])) { return 5; }
  if (!unchanged([wrong_name, valid])) { return 6; }
  if (!unchanged([valid, wrong_arity])) { return 7; }
  return 0;
}
`

func TestSelfHostPrinterAssignmentFallback(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "printer.fern")
	writeResolveProject(t, dir, map[string]string{"assignment_contract.fern": printerAssignmentContract})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "assignment_contract.fern", "assignment_contract", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil || len(out) != 0 {
				t.Fatalf("printer assignment fallback: %v\n%s", err, out)
			}
		})
	}
}
