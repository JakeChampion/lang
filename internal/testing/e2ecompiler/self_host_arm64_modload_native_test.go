package e2ecompiler

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const arm64ImageSpan = 1 << 27

func TestSelfHostArm64ModloadNativeBuild(t *testing.T) {
	fernBin := buildFernCLIBin(t)
	dir := writeSelfHostModloadProject(t)
	entry := filepath.Join(dir, "drivers/asm_modload_run.fern")
	orch := filepath.Join(dir, "orch_arm64")

	if err := withBuildMemoryMB(arm64ImageBuildMB, func() error {
		out, err := exec.Command(fernBin, "-target", "arm64-linux", "-o", orch, entry).CombinedOutput()
		if err != nil {
			return &buildErr{err: err, out: out}
		}
		return nil
	}); err != nil {
		t.Fatalf("arm64 build of the per-module orchestrator failed: %v", err)
	}

	raw, err := os.ReadFile(orch)
	if err != nil {
		t.Fatalf("read image: %v", err)
	}
	f, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("output is not a parseable ELF: %v", err)
	}
	if f.Machine != elf.EM_AARCH64 || f.Type != elf.ET_EXEC {
		t.Fatalf("got machine=%v type=%v, want AARCH64/EXEC", f.Machine, f.Type)
	}
	var code uint64
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && p.Flags&elf.PF_X != 0 {
			code = p.Memsz
		}
	}
	if code == 0 {
		t.Fatal("no executable PT_LOAD segment in the image")
	}
	if code > arm64ImageSpan {
		t.Logf("code segment %d bytes — %d past the ±128 MB b/bl span, so this build is veneered", code, code-arm64ImageSpan)
	} else {
		t.Logf("code segment %d bytes — %d below the ±128 MB b/bl span", code, arm64ImageSpan-code)
	}

	t.Run("executes", func(t *testing.T) {
		gcc, qemu := arm64Tooling(t)
		prog := filepath.Join(dir, "prog.fern")
		if err := os.WriteFile(prog, []byte("function main(): i32 { return 42; }\n"), 0o644); err != nil {
			t.Fatalf("write program: %v", err)
		}
		cmd := runArm64Bin(qemu, orch, prog, "-target", "arm64-linux")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		asm, err := cmd.Output()
		if err != nil {
			t.Fatalf("running the arm64 orchestrator: %v\n%s", err, stderr.String())
		}
		compiled := buildBinArm64(t, gcc, dir, "prog", string(asm))
		run := runArm64Bin(qemu, compiled)
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 42 {
			t.Fatalf("program built by the arm64 orchestrator exited %d, want 42", got)
		}
	})
}

const arm64ImageBuildMB = 8500

type buildErr struct {
	err error
	out []byte
}

func (e *buildErr) Error() string { return e.err.Error() + "\n" + string(e.out) }

func buildFernCLIBin(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fern")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern").CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	return bin
}
