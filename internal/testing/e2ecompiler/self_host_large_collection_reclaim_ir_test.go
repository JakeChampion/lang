package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostLargeCollectionReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	prog := `function build(pre: string): i32 {
  let xs: string[] = [pre + "a"];
  let i: i32 = 0;
  while (i < 70000) { xs = xs.append(pre + "b"); i = i + 1; }
  return xs.len();
}
function churn(n: i32): i32 {
  let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0;
  while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; }
  return acc;
}
function main(): i32 {
  let w: i32 = churn(3);
  let b1: i32 = (__heap_bump_bytes() as i32);
  let x: i32 = churn(3);
  let b2: i32 = (__heap_bump_bytes() as i32);
  if (__rc_underflow_count() != 0) { return 99; }
  if (b2 - b1 >= 1048576) { return 98; }
  if (w != x) { return 97; }
  return 0;
}`

	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	bin := buildBin(t, gcc, dir, "large-collection-reclaim", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("large-collection-reclaim exited %d, want 0 (98 = >=512 KiB outer buffer leaked → bump grew; 99 = over-release; 97 = value corrupted)", code)
	}
}
