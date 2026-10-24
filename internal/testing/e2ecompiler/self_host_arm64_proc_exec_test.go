package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostArm64ProcExecRuns(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)
	if len(x86runner) != 0 {
		t.Skip("the modload driver takes its entry as an argv path; needs a native host run")
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "proc_exec.fern")
	prog := "function main(): i32 {\n" +
		"    let rc: i32 = proc_exec(\"/bin/sh\", [\"-c\", \"exit 9\"]);\n" +
		"    return 1;\n" +
		"}\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	for _, c := range []struct {
		name string
		args []string
	}{
		{"permodule", []string{"-target", "arm64-linux"}},
		{"wholeprogram", []string{"-target", "arm64-linux", "-per-module-emit", "0"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			args := c.args
			if c.name == "wholeprogram" {
				needs := runDriverFile(t, x86runner, driverBin, src, "-target", "arm64-linux", "-per-module-needs")
				for _, n := range strings.Split(string(needs), "\n") {
					if n = strings.TrimSpace(n); n != "" {
						args = append(args, "-extra-need", n)
					}
				}
			}
			asm := string(runDriverFile(t, x86runner, driverBin, src, args...))
			if !strings.Contains(asm, "__fn___fern_proc_exec:") {
				t.Fatalf("%s emit has no Fern __fn___fern_proc_exec helper", c.name)
			}
			if strings.Contains(asm, "\n__fern_proc_exec:") {
				t.Fatalf("%s: the register-ABI hand-asm __fern_proc_exec is back", c.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "proc_exec_"+c.name, asm)
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != 9 {
				out, _ := runArm64Bin(qemu, bin).CombinedOutput()
				t.Fatalf("%s: proc_exec program exited %d, want 9 (exec of /bin/sh -c 'exit 9')\n%s", c.name, got, out)
			}
		})
	}
}
