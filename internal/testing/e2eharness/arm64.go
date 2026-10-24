package e2eharness

import (
	"os/exec"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

func Arm64Tooling(t *testing.T) (gcc, qemu string) {
	t.Helper()
	gcc, qemu, ok := LookupArm64Tooling()
	if !ok {
		if envflag.On("FERN_REQUIRE_ARM64_TOOLING") {
			t.Fatalf("aarch64 cross toolchain not available (gcc=%q qemu=%q) and FERN_REQUIRE_ARM64_TOOLING=1: "+
				"this lane installs it and is the only one running this test, so a skip here covers nothing", gcc, qemu)
		}
		t.Skipf("aarch64 cross toolchain not available (gcc=%q qemu=%q)", gcc, qemu)
	}
	return gcc, qemu
}

func Arm64Runner(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		return ""
	}
	_, qemu, _ := LookupArm64Tooling()
	if qemu == "" {
		if envflag.On("FERN_REQUIRE_ARM64_TOOLING") {
			t.Fatal("no qemu-aarch64 on PATH and FERN_REQUIRE_ARM64_TOOLING=1: this lane installs it and is the only one running this test, so a skip here covers nothing")
		}
		t.Skip("no qemu-aarch64 on PATH")
	}
	return qemu
}

func Arm64Assembler(t *testing.T) string {
	t.Helper()
	gcc, _, _ := LookupArm64Tooling()
	if gcc == "" {
		if envflag.On("FERN_REQUIRE_ARM64_TOOLING") {
			t.Fatal("no aarch64 cross gcc on PATH and FERN_REQUIRE_ARM64_TOOLING=1: this lane installs it and is the only one running this test, so a skip here covers nothing")
		}
		t.Log("no aarch64 cross gcc on PATH: assembling nothing")
	}
	return gcc
}

func LookupArm64Tooling() (gcc, qemu string, ok bool) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		if p, err := exec.LookPath("gcc"); err == nil {
			return p, "", true
		}
	}
	for _, c := range []string{"aarch64-linux-gnu-gcc", "aarch64-unknown-linux-gnu-gcc"} {
		if p, err := exec.LookPath(c); err == nil {
			gcc = p
			break
		}
	}
	for _, c := range []string{"qemu-aarch64", "qemu-aarch64-static"} {
		if p, err := exec.LookPath(c); err == nil {
			qemu = p
			break
		}
	}
	return gcc, qemu, gcc != "" && qemu != ""
}

func RunArm64Bin(qemu, binPath string, args ...string) *exec.Cmd {
	if qemu == "" {
		return exec.Command(binPath, args...)
	}
	return exec.Command(qemu, append([]string{binPath}, args...)...)
}

func CompileAndRunArm64(t *testing.T, src string) (stdout string, exitCode int) {
	t.Helper()
	binPath, qemu := CompileArm64Bin(t, src)
	cmd := RunArm64Bin(qemu, binPath)
	out, _ := cmd.CombinedOutput()
	return finishArm64Run(t, cmd, out)
}

func CompileAndRunArm64HighHeap(t *testing.T, src string) (stdout string, exitCode int) {
	t.Helper()
	binPath, qemu := compileArm64Bin(t, src, true)
	cmd := RunArm64Bin(qemu, binPath)
	out, _ := cmd.CombinedOutput()
	return finishArm64Run(t, cmd, out)
}

func CompileArm64Bin(t *testing.T, src string) (binPath, qemu string) {
	t.Helper()
	return compileArm64Bin(t, src, false)
}

func compileArm64Bin(t *testing.T, src string, highHeap bool) (binPath, qemu string) {
	t.Helper()
	qemu = Arm64Runner(t)
	var env []string
	if highHeap {
		env = []string{"FERN_HIGH_HEAP=1"}
	}
	return CompileSelfHostSource(t, TargetArm64Linux, src, env), qemu
}

func finishArm64Run(t *testing.T, cmd *exec.Cmd, out []byte) (string, int) {
	t.Helper()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("arm64 binary did not exit normally (out=%q)", out)
	}
	return string(out), cmd.ProcessState.ExitCode()
}
