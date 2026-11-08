package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// Every terminator kind has a name, so a refusal that names one is never
// "ssa terminator " with nothing after it. Only `ret` and `brif` read a
// value: the zero in the value field of `br` and `unreachable` is not a use
// of v0, for the certifier as for every other pass.
func TestSelfHostSsaTerminators(t *testing.T) {
	const src = `import "./ssa";
function main(): i32 {
  let want = ["", "ret", "br", "brif", "unreachable"];
  for k in 0..want.len() {
    if (ssa.term_name(k) != want[k]) {
      print(ssa.term_name(k));
      return k + 1;
    }
  }
  if (ssa.term_name(9) != "terminator 9") {
    print(ssa.term_name(9));
    return 10;
  }
  let one = ssa.SInst { kind_tag: 1, result: 0, args: [], imm: 0, str: "1" };
  let enter = ssa.SBlock { id: 0, preds: [], insts: [one], term: ssa.STerm { kind_tag: 2, cond: 0, target: 1, t: 0, f: 0, value: 0 } };
  let leave = ssa.SBlock { id: 1, preds: [0], insts: [], term: ssa.STerm { kind_tag: 4, cond: 0, target: 0, t: 0, f: 0, value: 0 } };
  let f = ssa.SFunc { name: "jump", nparams: 0, nvals: 1, entry: 0, takes_env: false, blocks: [enter, leave] };
  let uses = ssa.use_counts(f);
  if (uses[0] != 0) {
    return 20 + uses[0];
  }
  return 0;
}`
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "ssa.fern")
	if err := os.WriteFile(filepath.Join(dir, "term-names.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "term-names.fern", "term-names")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("term names: %v\n%s", err, out)
	}
}
