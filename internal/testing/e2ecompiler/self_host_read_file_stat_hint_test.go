package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

const readFilePseudoSrc = `function main(): i32 {
  let a: i32 = 0 - 1;
  let b: i32 = 0 - 2;
  match (read_file("/proc/self/mounts")) { Ok(s) => { a = s.len(); }, Err(e) => { a = 0 - 3; } }
  match (read_file_bytes("/proc/self/mounts")) { Ok(v) => { b = v.len(); }, Err(e) => { b = 0 - 4; } }
  if (a <= 0) { return 1; }
  if (a != b) { return 2; }
  return 0;
}`

func TestSelfHostReadFilePseudoFileX86_64(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("no procfs on " + runtime.GOOS + "; this test's whole subject is /proc")
	}
	if _, err := os.Stat("/proc/self/mounts"); err != nil {
		t.Fatalf("/proc/self/mounts unreadable (%v); this test's whole subject is procfs", err)
	}
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
	asm := runCapture(t, gcc, runner, driverBin, []byte(readFilePseudoSrc))
	progBin := buildBin(t, gcc, dir, "rf_pseudo", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run()
	switch code := cmd.ProcessState.ExitCode(); code {
	case 0:
	case 1:
		t.Fatalf("read_file(/proc/self/mounts) came back empty — the read is still sized and measured by st_size")
	case 2:
		t.Fatalf("read_file and read_file_bytes disagree on the length of /proc/self/mounts")
	default:
		t.Fatalf("exit %d, stdout %q", code, stdout.String())
	}
}
