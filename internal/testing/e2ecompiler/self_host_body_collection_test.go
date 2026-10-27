package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const bodyCollectionContractHook = `
pub function contract_body_collections(): i32 {
  let mod = parser.parse_module(lexer.tokenize("function live(x: i64): i64 { return x; }"));
  let inf = produced_rows(mod, irtables.struct_tab_empty(), semsource.plain_build());
  let proto = inf.built.decls[0];
  let data = match (inf.rows.body[0]) { Ok(body) => match (body) { Some(r) => r, None => { exit(1); } }, Err(_) => { exit(2); } };
  let partial = Err(declined_state("body refusal"));
  let built = semsource.Built {
    ...inf.built,
    decls: [proto, semsource.Produced { ...proto, dropped: true }, semsource.Produced { ...proto, template: true }, semsource.Produced { ...proto, template: true, ok: false, bodyless: true }, semsource.Produced { ...proto, ok: false, bodyless: true }, semsource.Produced { ...proto, ok: false }],
    instances: [semsource.Produced { ...proto, dropped: true }, proto, semsource.Produced { ...proto, ok: false }],
    names: ["live", "dropped", "template", "unused_template", "extern", "refused", "spliced_instance", "specialized", "failed_instance"],
  };
  let specialized = irtables.Lowered { ...data, name: "specialized", erased_wide: true, dbg_slots: [0], dbg_names: ["argument"], dbg_types: ["i64"] };
  let rows = Rows {
    ...inf.rows,
    body: [Ok(Some(data)), partial, partial, partial, partial, partial, partial, Ok(Some(specialized)), partial],
    keep: [true, false, false, false, false, false, false, true, false],
    why: ["", "", "", "", "", "declaration refusal", "", "", "instance refusal"],
  };
  let decl = declaration_bodies(built, rows);
  if (decl.count != 5 || decl.bodies.len() != 6) { return 3; }
  match (decl.bodies[0]) {
    Ok(body) => { match (body) { Some(r) => { if (r.name != data.name || r.n_params != data.n_params || r.ops.len() != data.ops.len()) { return 4; } }, None => { return 5; } } },
    Err(_) => { return 6; }
  }
  for i in 1..5 {
    match (decl.bodies[i]) { Ok(body) => { match (body) { Some(_) => { return 7; }, None => {} } }, Err(_) => { return 8; } }
  }
  match (decl.bodies[5]) { Ok(_) => { return 9; }, Err(r) => { if (r.why != "declaration refusal") { return 10; } } }
  let inst = instance_bodies(built, rows, decl.helpers);
  if (inst.unspliced != 1 || inst.bodies.len() != 1 || inst.origins.len() != 1 || inst.origins[0].producer != 0 || inst.origins[0].symbol != "specialized") { return 11; }
  match (inst.bodies[0]) {
    Ok(body) => { match (body) { Some(r) => { if (r.name != "specialized" || !r.erased_wide || r.dbg_names[0] != "argument" || r.n_params != data.n_params || r.ops.len() != data.ops.len()) { return 12; } }, None => { return 13; } } },
    Err(_) => { return 14; }
  }
  return 0;
}
`

func TestSelfHostBodyCollectionContracts(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "semlower.fern")
	f, err := os.OpenFile(filepath.Join(dir, "semlower.fern"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(bodyCollectionContractHook); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	writeResolveProject(t, dir, map[string]string{"body_collection_contract.fern": `import "./semlower";
function main(): i32 { return semlower.contract_body_collections(); }
`})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "body_collection_contract.fern", "body_collection_contract", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("body collection contract: %v\n%s", err, out)
			}
		})
	}
}
