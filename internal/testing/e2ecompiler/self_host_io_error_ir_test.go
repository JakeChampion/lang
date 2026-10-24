package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type ioErrorCase struct {
	name string
	src  string
	want int
}

func ioErrorCases(t *testing.T) []ioErrorCase {
	t.Helper()
	okPath := filepath.Join(t.TempDir(), "ok.txt")
	if err := os.WriteFile(okPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write ok file: %v", err)
	}
	regFile := filepath.Join(t.TempDir(), "reg.txt")
	if err := os.WriteFile(regFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write reg file: %v", err)
	}
	notDir := regFile + "/child"
	return []ioErrorCase{
		{"notfound-variant", `function main(): i32 { match (read_file("/nonexistent-fern-probe")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return 2; }, _ => { return 4; } } } } return 0; }`, 2},
		{"notfound-payload-len", `function main(): i32 { match (read_file("/nonexistent-fern-probe")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return p.len(); }, _ => { return 4; } } } } return 0; }`, 23},
		{"ok-control", `function main(): i32 { match (read_file("` + okPath + `")) { Ok(s) => { return s.len(); }, Err(_) => { return 90; } } return 0; }`, 6},
		{"writefile-notfound", `function main(): i32 { match (write_file("/nonexistent-dir-fern/x.txt", "hi")) { Err(e) => { match (e) { NotFound(p) => { return 2; }, _ => { return 4; } } }, Ok(_) => { return 1; } } return 0; }`, 2},
		{"writefile-ok-control", `function main(): i32 { match (write_file("` + filepath.Join(t.TempDir(), "out.txt") + `", "hi")) { Err(_) => { return 90; }, Ok(_) => { return 1; } } return 0; }`, 1},
		{"stat-notfound-payload-len", `function main(): i32 { match (stat("/nonexistent-fern-probe")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return p.len(); }, _ => { return 4; } } } } return 0; }`, 23},
		{"stat-notdir-is-other", `function main(): i32 { match (stat("` + notDir + `")) { Ok(_) => { return 1; }, Err(e) => { match (e) { Other(_, _, _) => { return 5; }, NotFound(_) => { return 6; }, _ => { return 7; } } } } return 0; }`, 5},
		{"readfile-notdir-is-other", `function main(): i32 { match (read_file("` + notDir + `")) { Ok(_) => { return 1; }, Err(e) => { match (e) { Other(_, _, _) => { return 5; }, NotFound(_) => { return 6; }, _ => { return 7; } } } } return 0; }`, 5},
		{"removefile-notdir-is-other", `function main(): i32 { match (remove_file("` + notDir + `")) { Err(e) => { match (e) { Other(_, _, _) => { return 5; }, NotFound(_) => { return 6; }, _ => { return 7; } } }, Ok(_) => { return 1; } } return 0; }`, 5},
		{"removefile-notfound", `function main(): i32 { match (remove_file("/nonexistent-fern-probe")) { Err(e) => { match (e) { NotFound(p) => { return 2; }, _ => { return 4; } } }, Ok(_) => { return 1; } } return 0; }`, 2},
		{"chroot-notfound-payload-len", `function main(): i32 { match (chroot("/nonexistent-fern-probe")) { Ok(_) => { return 1; }, Err(e) => { match (e) { NotFound(p) => { return p.len(); }, _ => { return 4; } } } } return 0; }`, 23},
		{"chroot-notdir-is-other", `function main(): i32 { match (chroot("` + notDir + `")) { Ok(_) => { return 1; }, Err(e) => { match (e) { Other(_, _, _) => { return 5; }, NotFound(_) => { return 6; }, _ => { return 7; } } } } return 0; }`, 5},
		{"setuid-unset-id-is-einval", `function main(): i32 { match (setuid(0i64 - 1i64)) { Ok(_) => { return 1; }, Err(e) => { match (e) { Other(_, _, _) => { return 5; }, _ => { return 7; } } } } return 0; }`, 5},
		{"setgid-over-32-bits-is-einval", `function main(): i32 { match (setgid(4294967296i64)) { Ok(_) => { return 1; }, Err(e) => { match (e) { Other(_, _, _) => { return 5; }, _ => { return 7; } } } } return 0; }`, 5},
		{"setgroups-bad-element-is-einval", `function main(): i32 { match (setgroups([4294967296i64, 0i64])) { Ok(_) => { return 1; }, Err(e) => { match (e) { Other(_, _, _) => { return 5; }, _ => { return 7; } } } } return 0; }`, 5},
	}
}

func TestSelfHostIoErrorIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	cases := ioErrorCases(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("%s: binary did not exit normally (segfault?)", tc.name)
			}
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostIoErrorIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range ioErrorCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(x86runner) == 0 {
				cmd = exec.Command(driverBin, "-target", "arm64-linux")
			} else {
				cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64-linux")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			if !bytes.Contains(asm, []byte("__fern_io_error")) {
				t.Fatalf("%s: emitted asm has no __fern_io_error — the error-path rework is not wired", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "ioe_"+tc.name, string(asm))
			run := runArm64Bin(qemu, bin)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally (segfault?)", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
