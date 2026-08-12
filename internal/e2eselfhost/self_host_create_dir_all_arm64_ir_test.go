package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostCreateDirAllArm64IR is the arm64 half of
// TestSelfHostCreateDirAllIR (#6749) — arm64-linux is the DEFAULT target, so a
// builtin that only works on x86-64 is a builtin most users cannot call.
//
// Both native self-host backends reach create_dir_all through the same Fern
// runtime source (asmcore.rt_src_create_dir_all), with only the mkdirat number
// and AT_FDCWD coming from the target, so this pins that the arm64 op dispatch
// and the fs-bundle gate actually emit it — and then runs the same chain under
// qemu to prove the shared body works there too.
func TestSelfHostCreateDirAllArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    match (temp_dir("fern-mkdirp-arm64")) {
        Ok(d) => {
            match (create_dir_all(d + "/one/two/three")) { Err(_) => { return 1; }, Ok(_) => {}, }
            match (write_file(d + "/one/two/three/deep.txt", "deep")) { Err(_) => { return 2; }, Ok(_) => {}, }
            match (read_file(d + "/one/two/three/deep.txt")) {
                Ok(s) => { if (s != "deep") { return 3; } },
                Err(_) => { return 4; },
            }
            match (create_dir_all(d + "/one/two/three")) { Err(_) => { return 5; }, Ok(_) => {}, }
            match (write_file(d + "/blocker", "x")) { Err(_) => { return 6; }, Ok(_) => {}, }
            match (create_dir_all(d + "/blocker/inner")) { Ok(_) => { return 7; }, Err(_) => {}, }
            match (remove_dir_all(d)) { Err(_) => { return 8; }, Ok(_) => {}, }
            return 0;
        },
        Err(_) => { return 9; },
    }
}`

	driverArgs := []string{"-target", "arm64-linux", "-ir"}
	var cmd *exec.Cmd
	if len(x86runner) == 0 {
		cmd = exec.Command(driverBin, driverArgs...)
	} else {
		cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), driverArgs...)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !strings.Contains(string(asm), "__fern_create_dir_all") {
		t.Fatal("create_dir_all did not reach the arm64 IR runtime path (no __fern_create_dir_all in asm)")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "mkdirp_arm64", string(asm))
	inner := runArm64Bin(qemu, bin)
	_ = inner.Run()
	if inner.ProcessState == nil || !inner.ProcessState.Exited() {
		t.Fatalf("inner did not exit normally")
	}
	if code := inner.ProcessState.ExitCode(); code != 0 {
		t.Errorf("create_dir_all arm64 IR program exited %d, want 0", code)
	}
}
