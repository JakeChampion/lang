package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const unionLookupProg = `import "./lexer";
import "./parser";
import "./checker";

function main(): i32 {
  let mod = parser.parse_module(lexer.tokenize("enum Box[T] { Full(T), Empty }\nenum UnitOnly { Unit }\nstruct A {}\nstruct B {}\ntype Choice = A | B;\nfunction main(): i32 { return 0; }\n"));
  let s = checker.module_scope(checker.query_view(mod));
  let box = match (checker.union_sig(s, "Box")) { Some(sig) => sig, None => { return 11; } };
  // The query view assigns internal names to generic parameters.
  if (box.name != "Box" || !box.is_enum || box.is_try || box.type_params.len() != 1 || box.type_params[0] != "0_T") { return 1; }
  if (box.variants.len() != 2 || box.variants[0] != "Full" || box.variants[1] != "Empty") { return 2; }
  let unit = match (checker.union_sig(s, "UnitOnly")) { Some(sig) => sig, None => { return 12; } };
  if (unit.name != "UnitOnly" || !unit.is_enum || unit.type_params.len() != 0 || unit.variants.len() != 1 || unit.variants[0] != "Unit") { return 3; }
  let choice = match (checker.union_sig(s, "Choice")) { Some(sig) => sig, None => { return 13; } };
  if (choice.name != "Choice" || choice.is_enum || choice.variants.len() != 2 || choice.variants[0] != "A" || choice.variants[1] != "B") { return 4; }
  if let Some(_) = checker.union_sig(s, "Missing") { return 5; }
  if let Some(_) = checker.enum_sig(s, "Missing") { return 5; }
  let result = match (checker.enum_sig(s, "Result")) { Some(sig) => sig, None => { return 14; } };
  if (result.name != "Result" || !result.is_enum || result.type_params.len() != 2 || result.type_params[0] != "T" || result.type_params[1] != "E") { return 6; }
  if (result.variants.len() != 2 || result.variants[0] != "Ok" || result.variants[1] != "Err") { return 7; }
  let option = match (checker.enum_sig(s, "Option")) { Some(sig) => sig, None => { return 15; } };
  if (option.name != "Option" || !option.is_enum || option.variants.len() != 2 || option.variants[0] != "Some" || option.variants[1] != "None") { return 8; }
  return 0;
}
`

func TestSelfHostUnionLookupContracts(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "checker.fern")
	writeResolveProject(t, dir, map[string]string{"union_lookup.fern": unionLookupProg})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "union_lookup.fern", "union_lookup", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("union lookup contract: %v\n%s", err, out)
			}
		})
	}
}
