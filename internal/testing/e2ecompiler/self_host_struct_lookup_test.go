package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const structLookupProg = `import "./lexer";
import "./parser";
import "./checker";

function main(): i32 {
  let source = "struct Point { x: i32, label: string }\nstruct Empty {}\nstruct Box[T] { value: T }\nenum E { EmptyCase, Data(i32), Record { x: i32 } }\ntype Mixed = Point | string;\nfunction main(): i32 { return 0; }\n";
  let s = checker.module_scope(checker.query_view(parser.parse_module(lexer.tokenize(source))));
  let Some(point) = checker.struct_sig(s, "Point") else { return 10; };
  if (point.name != "Point" || point.enum_owner != "" || point.module != "" || point.opaque || point.type_params.len() != 0 || point.derives.len() != 0) { return 1; }
  if (point.field_names.len() != 2 || point.field_names[0] != "x" || point.field_names[1] != "label" || point.field_types.len() != 2 || point.field_type_names.len() != 2) { return 2; }
  if (checker.type_label(point.field_types[0]) != "i32" || checker.type_label(point.field_types[1]) != "string" || point.field_type_names[0] != "i32" || point.field_type_names[1] != "string") { return 3; }
  let Some(empty) = checker.struct_sig(s, "Empty") else { return 11; };
  if (empty.name != "Empty" || empty.field_names.len() != 0 || empty.field_types.len() != 0) { return 4; }
  let Some(box) = checker.struct_sig(s, "Box") else { return 12; };
  if (box.type_params.len() != 1 || box.field_names.len() != 1 || box.field_names[0] != "value" || box.field_type_names[0] != box.type_params[0]) { return 5; }
  let Some(data) = checker.variant_sig(s, "Data", "E") else { return 13; };
  if (data.name != "Data" || data.enum_owner != "E" || data.field_names.len() != 1 || data.field_names[0] != "__ev" || checker.type_label(data.field_types[0]) != "i32") { return 6; }
  let Some(record) = checker.variant_sig(s, "Record", "E") else { return 14; };
  if (record.enum_owner != "E" || record.variant_field_names.len() != 1 || record.variant_field_names[0] != "x") { return 7; }
  let Some(plain) = checker.variant_sig(s, "Point", "") else { return 15; };
  let Some(shape) = checker.variant_shape_sig(s, "Point", "Mixed") else { return 16; };
  if (plain.name != "Point" || shape.name != "Point") { return 8; }
  if let Some(_) = checker.struct_sig(s, "missing") { return 9; }
  if let Some(_) = checker.variant_sig(s, "Data", "Other") { return 9; }
  if let Some(_) = checker.variant_shape_sig(s, "Data", "Other") { return 9; }
  let params = checker.struct_type_params(s, "Box");
  let names = checker.struct_field_names(s, "Point");
  if (params.len() != 1 || params[0] != box.type_params[0] || names.len() != 2 || names[0] != "x") { return 17; }
  if (checker.struct_field_names(s, "missing").len() != 0 || checker.struct_type_params(s, "missing").len() != 0 || s.has_struct("missing") || !s.has_struct("Empty")) { return 18; }
  return 0;
}
`

func TestSelfHostStructLookupContracts(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "checker.fern")
	writeResolveProject(t, dir, map[string]string{"struct_lookup.fern": structLookupProg})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "struct_lookup.fern", "struct_lookup", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("struct lookup contract: %v\n%s", err, out)
			}
		})
	}
}
