package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostLoweredUnitSignatureGate(t *testing.T) {
	dir := copySelfHostTree(t)
	const source = `import "./ir";
import "./irverify";
import "./irverifygate";
import "./rccertify";
import "./ssa_lift";

function calls(g: irverifygate.Gate, name: string, count: i32): irverify.Problem[] {
  let ops: ir.Op[] = [];
  for i in 0..count { ops = ops.append(ir.op_const_i32(i)); }
  ops = ops.append(ir.op_call_direct(name, count)).append(ir.op_return());
  return irverifygate.verify_problems("caller", ops, 0, 0, g);
}

function main(): i32 {
  let declared = irverify.sig_index([
    irverify.FnSig { name: "external", n_params: 3 },
    irverify.FnSig { name: "local", n_params: 3 },
  ]);
  let physical = rccertify.abis_of(["external"], [[1, 1, 1, 1, 1, 1]], [false], [false], [-2], [[-2, -2]], [false]);
  let gate = irverifygate.gate_unreported(declared, ssa_lift.kind_table(), physical);
  if (calls(gate, "external", 6).len() != 0) { return 1; }
  if (irverify.index_params(gate.sigs, "external") != 6) { return 2; }
  if (calls(gate, "external", 3).len() != 1) { return 3; }
  if (calls(gate, "external", 8).len() != 1) { return 4; }
  if (calls(gate, "local", 3).len() != 0) { return 5; }
  if (calls(gate, "local", 6).len() != 1) { return 6; }
  let fallback = irverifygate.gate_unreported(declared, ssa_lift.kind_table(), rccertify.no_abis());
  if (calls(fallback, "external", 3).len() != 0) { return 7; }
  if (calls(fallback, "external", 6).len() != 1) { return 8; }
  return 0;
}
`
	entry := filepath.Join(dir, "unit_signature_gate.fern")
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, entry, target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("signature gate exit %d\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
