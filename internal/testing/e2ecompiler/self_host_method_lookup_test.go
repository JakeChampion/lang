package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const methodLookupProg = `import "./lexer";
import "./parser";
import "./checker";

function main(): i32 {
  let source = "struct Point { x: i32 }\nfunction (p: Point) read(n: i32): i32 { return p.x + n; }\nfunction (xs: i32[]) first(): i32 { return xs[0]; }\nfunction (m: Map[string, i32]) __iterator(): i32 { return 0; }\nfunction main(): i32 { return 0; }\n";
  let s = checker.module_scope(checker.query_view(parser.parse_module(lexer.tokenize(source))));
  if let Some(point) = checker.method_sig(s, "read", "Point") {
  if (point.name != "read" || point.receiver_type != "Point" || point.param_types.len() != 1 || point.param_type_names.len() != 1 || point.param_type_names[0] != "i32") { return 1; }
  if (checker.type_label(point.ret_type) != "i32" || point.ret_type_name != "i32" || checker.type_label(point.param_types[0]) != "i32") { return 2; }
  if (point.line != 2 || point.col != 1 || point.module != "" || point.impl_trait != "" || point.file != "") { return 3; }
  } else { return 8; }
  if let Some(_) = checker.method_sig(s, "missing", "Point") { return 4; }
  if let Some(_) = checker.method_sig(s, "read", "Other") { return 4; }
  if let Some(array) = checker.array_method_sig(s, "first", false) {
  if (array.name != "first" || array.receiver_type != "i32[]" || array.param_types.len() != 0 || checker.type_label(array.ret_type) != "i32") { return 5; }
  } else { return 9; }
  if let Some(_) = checker.array_method_sig(s, "first", true) { return 6; }
  if let Some(_) = checker.array_method_sig(s, "missing", false) { return 6; }
  if let Some(provider) = checker.method_sig(s, "__iterator", "Map") {
  if let Some(iterator) = checker.method_sig(s, "iter", "Map") {
  if (provider.name != "__iterator" || iterator.name != provider.name || iterator.receiver_type != provider.receiver_type || iterator.line != provider.line || iterator.ret_type_name != provider.ret_type_name) { return 7; }
  } else { return 10; }
  } else { return 11; }
  return 0;
}
`

func TestSelfHostMethodLookupContracts(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "checker.fern")
	writeResolveProject(t, dir, map[string]string{"method_lookup.fern": methodLookupProg})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "method_lookup.fern", "method_lookup", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("method lookup contract: %v\n%s", err, out)
			}
		})
	}
}
