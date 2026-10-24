package e2eharness

import (
	"os/exec"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

func X86_64Tooling(t testing.TB) (gcc string, exec_ []string) {
	t.Helper()
	gcc, exec_, ok := LookupX86_64Tooling()
	if !ok {
		why := "non-x86_64 host and no qemu-x86_64 on PATH"
		if gcc == "" {
			why = "no x86_64-linux-gnu-gcc / gcc on PATH"
		}
		if envflag.On("FERN_REQUIRE_X86_64_TOOLING") {
			t.Fatalf("%s and FERN_REQUIRE_X86_64_TOOLING=1: this lane has the "+
				"toolchain and is the only one running this test, so a skip here covers nothing", why)
		}
		t.Skipf("%s; skipping x86-64 e2e", why)
	}
	return gcc, exec_
}

func X86_64Runner(t testing.TB) []string {
	t.Helper()
	if runner, ok := lookupX86_64Runner(); ok {
		return runner
	}
	if envflag.On("FERN_REQUIRE_X86_64_TOOLING") {
		t.Fatal("non-x86_64 host and no qemu-x86_64 on PATH, and FERN_REQUIRE_X86_64_TOOLING=1: this lane has the toolchain and is the only one running this test, so a skip here covers nothing")
	}
	t.Skip("non-x86_64 host and no qemu-x86_64 on PATH; skipping x86-64 e2e")
	return nil
}

func LookupX86_64Tooling() (gcc string, exec_ []string, ok bool) {
	if p, err := exec.LookPath("x86_64-linux-gnu-gcc"); err == nil {
		gcc = p
	} else if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		if p, err := exec.LookPath("gcc"); err == nil {
			gcc = p
		}
	}
	if gcc == "" {
		return "", nil, false
	}
	if runner, ok := lookupX86_64Runner(); ok {
		return gcc, runner, true
	}
	if InterpDriverMode() {
		return gcc, nil, true
	}
	return gcc, nil, false
}

func lookupX86_64Runner() (runner []string, ok bool) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return nil, true
	}
	if p, err := exec.LookPath("qemu-x86_64"); err == nil {
		return []string{p}, true
	}
	return nil, false
}

func RunX86_64Bin(runner []string, binPath string, args ...string) *exec.Cmd {
	if len(runner) == 0 {
		return exec.Command(binPath, args...)
	}
	argv := append(append(append([]string{}, runner[1:]...), binPath), args...)
	return exec.Command(runner[0], argv...)
}

func CompileAndRunX86_64(t testing.TB, src string) (stdout string, exitCode int) {
	t.Helper()
	binPath, runner := CompileX86_64Bin(t, src)
	cmd := RunX86_64Bin(runner, binPath)
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

func CompileX86_64Bin(t testing.TB, src string) (binPath string, runner []string) {
	t.Helper()
	runner = X86_64Runner(t)
	return CompileSelfHostSource(t, TargetX86_64Linux, src, nil), runner
}

const x86MachinePrefix = "x86_64-"
