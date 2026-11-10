package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const receiverUnionMembershipHook = `function contract_receiver_query(unions: UnionSig[], prototype: MethodSig, owner: string, variant: string, raw_want: boolean, want: boolean, imported: boolean): boolean {
  let table = union_table(unions);
  let ut = UnionTable { ...table, from_import: imported };
  let base = new_scope();
  let method = MethodSig { ...prototype, name: "probe", receiver_type: owner };
  let s = Scope { ...base, unions: ut, names_of_types: type_names_of(base.structs, ut, []), methods: method_table([method]) };
  if (is_variant_of(variant, owner, unions) != raw_want) { return false; }
  return s.receives(0, variant) == want && s.inherent_receives(0, variant, -1) == want && s.has_method("probe", variant) == want && s.lookup_method("probe", variant).is_some() == want;
}

pub function contract_receiver_union_membership(): i32 {
  let mod = parser.parse_module(lexer.tokenize("struct Part { value: i32 } struct Other { value: i32 } type ContractUnion = Part | Other; function (value: ContractUnion) probe(): i32 { return 0; }"));
  let env = module_env(mod);
  let at = env.unions.index.find("ContractUnion");
  if (at < 0 || env.methods.methods.len() != 1) { return 90; }
  let original = env.unions.unions[at];
  let prototype = env.methods.methods[0];
  let a = UnionSig { ...original, name: "U", variants: ["A"] };
  let b = UnionSig { ...original, name: "U", variants: ["B"] };
  let empty = UnionSig { ...original, name: "U", variants: [] };
  let blank_variant = UnionSig { ...original, name: "U", variants: [""] };
  let blank_owner = UnionSig { ...original, name: "", variants: ["Member"] };
  let qualified = UnionSig { ...original, name: "hidden__U", variants: ["hidden__Part"], module: "hidden", visible_from: [] };
  for pass in 0..2 {
    let imported = pass == 1;
    if (!contract_receiver_query([], prototype, "Missing", "Other", false, false, imported)) { return 1; }
    if (!contract_receiver_query([], prototype, "Direct", "Direct", false, true, imported)) { return 2; }
    if (!contract_receiver_query([], prototype, "Generic[i32]", "Generic", false, true, imported)) { return 3; }
    if (!contract_receiver_query([a], prototype, "U", "A", true, true, imported)) { return 4; }
    if (!contract_receiver_query([a], prototype, "U", "B", false, false, imported)) { return 5; }
    if (!contract_receiver_query([a, b], prototype, "U", "B", false, false, imported)) { return 6; }
    if (!contract_receiver_query([a, b], prototype, "U", "A", true, true, imported)) { return 7; }
    if (!contract_receiver_query([b, a], prototype, "U", "B", true, true, imported)) { return 8; }
    if (!contract_receiver_query([b, a], prototype, "U", "A", false, false, imported)) { return 9; }
    if (!contract_receiver_query([empty, b], prototype, "U", "B", false, false, imported)) { return 10; }
    if (!contract_receiver_query([blank_variant], prototype, "U", "", true, true, imported)) { return 11; }
    if (!contract_receiver_query([blank_owner], prototype, "", "Member", true, true, imported)) { return 12; }
    if (!contract_receiver_query([qualified], prototype, "hidden__U", "hidden__Part", true, true, imported)) { return 13; }
    if (!contract_receiver_query([qualified], prototype, "hidden__U", "Part", false, false, imported)) { return 14; }
    if (!contract_receiver_query([qualified], prototype, "U", "hidden__Part", false, false, imported)) { return 15; }
  }
  let wide: UnionSig[] = [];
  for i in 0..300 {
    let suffix = util.i32_to_string(i);
    wide = wide.append(UnionSig { ...original, name: "Wide" + suffix, variants: ["Value" + suffix] });
  }
  if (!contract_receiver_query(wide, prototype, "Wide0", "Value0", true, true, false)) { return 16; }
  if (!contract_receiver_query(wide, prototype, "Wide150", "Value150", true, true, true)) { return 17; }
  if (!contract_receiver_query(wide, prototype, "Wide299", "Value299", true, true, false)) { return 18; }
  if (!contract_receiver_query(wide, prototype, "Wide299", "Value0", false, false, true)) { return 19; }
  if (!contract_receiver_query(wide, prototype, "Wide300", "Value299", false, false, false)) { return 20; }
  return 0;
}
`

func TestSelfHostReceiverUnionMembership(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "checker.fern")
	f, err := os.OpenFile(filepath.Join(dir, "checker.fern"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n" + receiverUnionMembershipHook); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	writeResolveProject(t, dir, map[string]string{"receiver_union.fern": `import "./checker";
function main(): i32 { return checker.contract_receiver_union_membership(); }
`})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "receiver_union.fern", "receiver_union", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil || len(out) != 0 {
				t.Fatalf("receiver union contract: %v\n%s", err, out)
			}
		})
	}
}
