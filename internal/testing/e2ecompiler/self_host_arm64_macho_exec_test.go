package e2ecompiler

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

type machoRun struct {
	name     string
	main     string
	wantExit int
	wantData bool
}

func assertMachORuns(t *testing.T, c machoRun) {
	t.Helper()
	bin := selfHostMachOBytes(t, c.name, arm64NativeSrc(t)+"\n"+c.main)
	assertMachOBytesRun(t, c.name, bin, c.wantExit, c.wantData)
}

func assertMachOBytesRun(t *testing.T, name string, bin []byte, wantExit int, wantData bool) {
	t.Helper()
	f, err := macho.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("self-host output is not a parseable Mach-O: %v", err)
	}
	if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
		t.Fatalf("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
	}
	if wantData && f.Segment("__DATA") == nil {
		t.Fatalf("expected a __DATA segment")
	}

	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Logf("structural check only: %s/%s cannot exec an arm64 Mach-O", runtime.GOOS, runtime.GOARCH)
		return
	}
	binPath := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(binPath, bin, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	cmd := exec.Command(binPath)
	runErr := cmd.Run()
	ps := cmd.ProcessState
	if ps == nil || !ps.Exited() {
		t.Fatalf("Mach-O did not run to a normal exit (err=%v, state=%v)", runErr, ps)
	}
	if got := ps.ExitCode(); got != wantExit {
		t.Errorf("self-host arm64-darwin %s exit = %d, want %d", name, got, wantExit)
	}
}

func selfHostMachOBytes(t *testing.T, name, source string) []byte {
	t.Helper()
	runner, driverBin := machoDriver(t)
	wat := runCapture(t, "", runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatalf("wasm emitter produced 0 bytes for the %s driver", name)
	}
	watPath := filepath.Join(t.TempDir(), name+".wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	bin, err := exec.Command("wasmtime", "run", watPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run (driver): %v", err)
	}
	return bin
}

func machoDriver(t *testing.T) (runner []string, bin string) {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the self-host arm64-darwin Mach-O suite")
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return nil, darwinMachoDriver(t)
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	return runner, buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
}

var (
	darwinDriverOnce sync.Once
	darwinDriverPath string
	darwinDriverErr  error
)

func darwinMachoDriver(t *testing.T) string {
	t.Helper()
	darwinDriverOnce.Do(func() {
		fern := buildLangBinForInterp(t)
		dir, err := os.MkdirTemp("", "selfhost-darwin-driver-")
		if err != nil {
			darwinDriverErr = err
			return
		}
		copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
		bin := filepath.Join(dir, "wasm_run")
		out, err := exec.Command(fern, "-target", "arm64-darwin", "-o", bin,
			filepath.Join(dir, "drivers/wasm_run.fern")).CombinedOutput()
		if err != nil {
			darwinDriverErr = &driverBuildError{err: err, out: out}
			return
		}
		if err := os.Chmod(bin, 0o755); err != nil {
			darwinDriverErr = err
			return
		}
		darwinDriverPath = bin
	})
	if darwinDriverErr != nil {
		t.Fatalf("arm64-darwin build of the wasm_run driver failed: %v", darwinDriverErr)
	}
	return darwinDriverPath
}

type driverBuildError struct {
	err error
	out []byte
}

func (e *driverBuildError) Error() string { return e.err.Error() + "\n" + string(e.out) }
