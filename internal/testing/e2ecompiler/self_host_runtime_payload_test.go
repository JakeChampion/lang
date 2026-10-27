package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const runtimePayloadContractHook = `
import "./sourcepos";

pub function contract_runtime_payload(): i32 {
  let lowered = runtime_lowering("function dup(x: i64): i64 { return x; }");
  if (lowered.bodies.len() != 1 || lowered.declarations.len() != 1 || lowered.declarations[0] != "dup") { return 1; }
  let proto = lowered.bodies[0];
  let cached_failure = Err(irtables.LowerState { ...declined_state("ignored cached refusal"), name: "dup" });
  let first = Ok(irtables.Lowered { ...proto, name: "fresh" });
  let duplicate = Err(irtables.LowerState { ...declined_state("ignored duplicate refusal"), name: "fresh" });
  let last = Ok(irtables.Lowered {
    ...proto, name: "tail", n_params: 5, n_locals: 5, erased_wide: true,
    ops: [ir.Op { ...ir.op_const_i64(17i64), source: 123i64, decl: 19 }, ir.op_return()],
    arr_slots: [0], i64_slots: [1], f64_slots: [2], v128_slots: [3], str_slots: [4],
    dbg_slots: [1], dbg_names: ["value"], dbg_types: ["i64"],
    source_positions: sourcepos.SourceInfo { entry: sourcepos.SourcePos { line: 40, col: "1" }, values: [sourcepos.SourcePos { line: 41, col: "2" }], terms: [sourcepos.SourcePos { line: 42, col: "3" }] },
  });
  match (complete_runtime_helpers([proto], [cached_failure, first, duplicate, last])) {
    Ok(done) => {
      if (done.bodies.len() != 3 || done.helper_indices.len() != 2 || done.helper_indices[0] != 1 || done.helper_indices[1] != 3) { return 2; }
      if (done.bodies[0].name != "dup" || done.bodies[1].name != "fresh" || done.bodies[1].n_params != proto.n_params) { return 3; }
      let b = done.bodies[2];
      if (b.name != "tail" || b.n_params != 5 || b.n_locals != 5 || !b.erased_wide || b.result_kind != proto.result_kind || b.ops.len() != 2 || b.ops[0].i64_imm != 17i64 || b.ops[0].source != 123i64 || b.ops[0].decl != 19) { return 4; }
      if (b.arr_slots[0] != 0 || b.i64_slots[0] != 1 || b.f64_slots[0] != 2 || b.v128_slots[0] != 3 || b.str_slots[0] != 4 || b.dbg_slots[0] != 1 || b.dbg_names[0] != "value" || b.dbg_types[0] != "i64") { return 5; }
      if (b.source_positions.entry.line != 40 || b.source_positions.entry.col != "1" || b.source_positions.values[0].line != 41 || b.source_positions.values[0].col != "2" || b.source_positions.terms[0].line != 42 || b.source_positions.terms[0].col != "3") { return 6; }
    },
    Err(_) => { return 7; }
  }
  let bad = Err(irtables.LowerState { ...declined_state("selected refusal"), name: "broken" });
  match (complete_runtime_helpers([], [bad])) {
    Ok(_) => { return 8; },
    Err(refusal) => { if (refusal.name != "broken" || refusal.why != "selected refusal") { return 9; } }
  }
  let omitted = Err(irtables.LowerState { ...declined_state(""), name: "omitted", ok: true, superseded: true });
  match (complete_runtime_helpers([], [omitted])) {
    Ok(_) => { return 10; },
    Err(refusal) => { if (refusal.name != "omitted" || refusal.why != "body superseded") { return 11; } }
  }
  let empty = runtime_lowering("");
  if (empty.bodies.len() != 0 || empty.declarations.len() != 0 || empty.helper_types.len() != 0) { return 12; }
  return 0;
}
`

func TestSelfHostRuntimePayloadContracts(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "semlower.fern")
	f, err := os.OpenFile(filepath.Join(dir, "semlower.fern"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(runtimePayloadContractHook); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	writeResolveProject(t, dir, map[string]string{"runtime_payload_contract.fern": `import "./semlower";
function main(): i32 { return semlower.contract_runtime_payload(); }
`})
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "runtime_payload_contract.fern", "runtime_payload_contract", target)
			out, err := mvsDriverCommand(t, target, bin).CombinedOutput()
			if err != nil {
				t.Fatalf("runtime payload contract: %v\n%s", err, out)
			}
		})
	}
}
