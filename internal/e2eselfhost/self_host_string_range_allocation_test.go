package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// Allocation metadata follows operation identity when a merge renumbers
// extension tags. The range constructor copies; the byte sum only reads.
func TestSelfHostStringRangeAllocationMarker(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/ir_kind_run.fern")
	src := `import "./ir";
function main(): i32 {
  if (!ir.op_allocates(ir.op_str_from_bytes_range().kind_tag)) { return 1; }
  if (ir.op_allocates(ir.op_sum_bytes_array().kind_tag)) { return 2; }
  return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "allocation.fern"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "allocation.fern", "allocation")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("allocation classification: %v\n%s", err, out)
	}
}
