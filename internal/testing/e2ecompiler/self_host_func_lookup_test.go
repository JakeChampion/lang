package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const funcLookupProg = `import "./lexer";
import "./parser";
import "./checker";

function main(): i32 {
  let source = "const answer: i32 = 42;\nfunction add(n: i32): i32 { return n + 1; }\nfunction identity[T](n: T): T { return n; }\nfunction keep(own xs: i32[]): i32[] { return xs; }\nfunction main(): i32 { return 0; }\n";
  let s = checker.module_scope(checker.query_view(parser.parse_module(lexer.tokenize(source))));
  let Some(add) = checker.func_sig(s, "add") else { return 10; };
  if (add.name != "add" || add.receiver != "" || add.module != "" || add.is_const) { return 1; }
  if (add.param_types.len() != 1 || add.param_type_names.len() != 1 || add.param_type_names[0] != "i32" || add.param_names.len() != 1 || add.param_names[0] != "n") { return 2; }
  if (checker.type_label(add.ret_type) != "i32" || add.ret_type_name != "i32" || checker.type_label(add.param_types[0]) != "i32") { return 3; }
  if (add.param_own.len() != 0 || add.all_type_params.len() != 0 || add.type_params.len() != 0 || add.typevars.len() != 0 || add.refs.len() != 0) { return 4; }
  let Some(answer) = checker.func_sig(s, "answer") else { return 11; };
  if (!answer.is_const || checker.type_label(answer.ret_type) != "i32" || answer.param_types.len() != 0) { return 5; }
  let Some(identity) = checker.func_sig(s, "identity") else { return 12; };
  if (identity.all_type_params.len() != 1 || identity.type_params.len() != 0 || identity.typevars.len() != 1) { return 6; }
  let tp = identity.all_type_params[0];
  if (identity.typevars[0] != tp || identity.ret_type_name != tp || identity.param_type_names[0] != tp || identity.refs.len() != 2) { return 7; }
  let tps = checker.func_type_params(s, "identity");
  if (tps.len() != 1 || tps[0] != tp || checker.func_type_params(s, "missing").len() != 0) { return 14; }
  let Some(keep) = checker.func_sig(s, "keep") else { return 13; };
  if (keep.param_own.len() != 1 || !keep.param_own[0] || keep.param_type_names[0] != "i32[]" || keep.ret_type_name != "i32[]") { return 8; }
  if let Some(_) = checker.func_sig(s, "missing") { return 9; }
  if let Some(_) = checker.func_sig(s, "print") { return 9; }
  return 0;
}
`

func TestSelfHostFuncLookupContracts(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "checker.fern")
	writeResolveProject(t, dir, map[string]string{"func_lookup.fern": funcLookupProg})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "func_lookup.fern", "func_lookup", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("function lookup contract: %v\n%s", err, out)
			}
		})
	}
}
