package e2ecompiler

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostGateOptionContracts(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ircore.fern")
	source := `import "./ircore";
import "./physicalbody";
import "./semrecords";
` + strings.Split(lowerResultAlignmentProg, "function main(): i32 {")[0] + `
function main(): i32 {
  let av = args();
  let mode = util.digits_to_i32(av[1]);
  let mod = parser.parse_module(lexer.tokenize("function main(x: i64, y: i64): i64 { return x+y; }"));
  let state = irtables.LowerState {
    ...body(2, "stored_name", 2), n_locals: 7, erased_wide: true,
    ops: [ir.op_call_direct("helper", 0), ir.op_return()],
    arr_slots: [0], i64_slots: [1], f64_slots: [2], v128_slots: [3], str_slots: [4],
    dbg_slots: [1], dbg_names: ["value"], dbg_types: ["i64"],
    source_positions: sourcepos.SourceInfo { entry: sourcepos.SourcePos { line: 40, col: "1" }, values: [sourcepos.SourcePos { line: 41, col: "2" }], terms: [sourcepos.SourcePos { line: 42, col: "3" }] },
  };
  let row = irtables.lower_result(state);
  let helper = irtables.LowerResult { ...row, name: "helper", ops: [ir.op_const_i64(17i64), ir.op_return()] };
  let meta = physicalbody.Metadata { contracts: [], records: semrecords.no_records(), enums: [], finalizers: [] };
  let origins = physicalbody.Origins { producers: [meta], declarations: physicalbody.named_origins(["declared_name"], 0), extra: physicalbody.named_origins(["helper"], 0) };
  let sub = ircore.Sub { ...ircore.no_sub(), bodies: [row], extra: [helper], physical_origins: origins };
  if (mode == 1 || mode == 2) {
    let op = if (mode == 1) { ir.op_call_direct("unknown", 0) } else { ir.op_const_func(0, "unknown") };
    let bad = ircore.Sub { ...sub, bodies: [irtables.LowerResult { ...row, ops: [op, ir.op_return()] }] };
    match (ircore.gate(mod, bad)) { Some(_) => { return 1; }, None => {} }
    match (ircore.gate_program(mod, bad)) { Some(_) => { return 2; }, None => {} }
    return 0;
  }
  if (mode == 3) {
    ircore.gate(mod, ircore.no_sub());
    return 3;
  }
  let selected = match (ircore.gate(mod, sub)) { Some(g) => g, None => { exit(4); } };
  if (selected.cache.len() != 2 || ircore.lowered_digest(selected.cache) != ircore.lowered_digest([row, helper])) { return 5; }
  if (selected.cache[0].dbg_names[0] != "value" || selected.cache[0].dbg_types[0] != "i64") { return 6; }
  if (selected.physical.producers.len() != 1 || selected.physical.rows.len() != 2 || selected.physical.rows[0].producer != 0 || selected.physical.rows[0].symbol != "declared_name" || selected.physical.rows[1].symbol != "helper") { return 7; }
  let program = match (ircore.gate_program(mod, sub)) { Some(g) => g, None => { exit(8); } };
  if (program.im.funcs.len() != 1 || program.im.funcs[0].name != "main" || ircore.lowered_digest(program.cache) != ircore.lowered_digest(selected.cache) || program.physical.rows[0].symbol != "declared_name") { return 9; }
  for name in ["__fern_rc_inc", "__c_call2", "__fern_task_contract"] {
    let allowed = ircore.Sub { ...sub, bodies: [irtables.LowerResult { ...row, ops: [ir.op_call_direct(name, 0), ir.op_return()] }] };
    match (ircore.gate(mod, allowed)) { Some(_) => {}, None => { return 10; } }
  }
  let empty = parser.parse_module(lexer.tokenize(""));
  match (ircore.gate(empty, ircore.no_sub())) {
    Some(g) => { if (g.cache.len() != 0 || g.physical.rows.len() != 0) { return 11; } },
    None => { return 12; }
  }
  match (ircore.gate_program(empty, ircore.no_sub())) {
    Some(g) => { if (g.im.funcs.len() != 0 || g.cache.len() != 0) { return 13; } },
    None => { return 14; }
  }
  return 0;
}
`
	writeResolveProject(t, dir, map[string]string{"gate_option_contract.fern": source})
	cases := []struct {
		name    string
		mode    string
		strict  bool
		code    int
		message string
	}{
		{"eligible", "0", false, 0, ""},
		{"unknown-call", "1", false, 0, ""},
		{"unknown-function-value", "2", false, 0, ""},
		{"missing-body", "3", false, 3, "error: internal: no typed body for main"},
		{"strict-call", "1", true, 3, "FERN_STRICT_IR: main (call to unknown symbol unknown)"},
		{"strict-function-value", "2", true, 3, "FERN_STRICT_IR: main (function value unknown not defined)"},
	}
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			bin := buildSelfHostBinFor(t, dir, "gate_option_contract.fern", "gate_option_contract", target)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					cmd := mvsDriverCommand(t, target, bin)
					cmd.Args = append(cmd.Args, tc.mode)
					strict := "0"
					if tc.strict {
						strict = "1"
					}
					cmd.Env = append(os.Environ(), "FERN_STRICT_IR="+strict)
					if target == e2eharness.TargetWasm32Wasi {
						cmd.Args = append([]string{cmd.Args[0], "run", "--env", "FERN_STRICT_IR=" + strict}, cmd.Args[2:]...)
					}
					out, err := cmd.CombinedOutput()
					code := 0
					if err != nil {
						exit, ok := err.(*exec.ExitError)
						if !ok {
							t.Fatalf("gate command: %v\n%s", err, out)
						}
						code = exit.ExitCode()
					}
					if code != tc.code || !strings.Contains(string(out), tc.message) || tc.message == "" && len(out) != 0 {
						t.Fatalf("gate contract: exit %d, want %d, message %q\n%s", code, tc.code, tc.message, out)
					}
				})
			}
		})
	}
}
