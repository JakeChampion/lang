package e2eselfhost

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

// The arm64-darwin Mach-O family: a Fern program built from
// examples/self_host/arm64_native.fern assembles AArch64 machine code, wraps it
// in an ad-hoc-signed Mach-O with macho.fern, and writes the raw binary to
// stdout. Every host checks the bytes parse as an arm64 MH_EXECUTE; Apple
// Silicon also launches the binary and checks its exit code, which is the only
// coverage anywhere that a self-host-emitted Mach-O actually loads.
//
// That exec half needs a driver the host can run. The stock route builds
// wasm_run as an x86-64 Linux ELF, which Apple Silicon cannot exec and for
// which the macOS runner has no qemu, so every case here skipped on the one
// host that could have executed anything (#6849). On darwin/arm64 the driver is
// therefore built FOR the host through cmd/fern's in-process Mach-O path.

// machoRun is one case: a driver main() that writes a Mach-O to stdout, the
// exit code the produced binary must have, and the structural extras it claims.
type machoRun struct {
	// name labels the intermediate .wat and the written binary.
	name string
	// main is the driver's main(), concatenated onto arm64_native.fern.
	main string
	// wantExit is the exit code the produced Mach-O must exit with.
	wantExit int
	// wantData requires a __DATA segment — the cases that lay constants there.
	wantData bool
}

// assertMachORuns builds the case's Mach-O with the self-host toolchain,
// asserts it is a well-formed arm64 executable, and on Apple Silicon executes
// it and checks the exit code.
func assertMachORuns(t *testing.T, c machoRun) {
	t.Helper()
	bin := selfHostMachOBytes(t, c.name, arm64NativeSrc(t)+"\n"+c.main)
	assertMachOBytesRun(t, c.name, bin, c.wantExit, c.wantData)
}

// assertMachOBytesRun is the half of assertMachORuns that works on bytes the
// caller already has — the real-asm suite builds its driver per case.
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
		// A container the kernel refuses to load is THE failure this family
		// exists to catch (#6042), so it must not be a skip.
		t.Fatalf("Mach-O did not run to a normal exit (err=%v, state=%v)", runErr, ps)
	}
	if got := ps.ExitCode(); got != wantExit {
		t.Errorf("self-host arm64-darwin %s exit = %d, want %d", name, got, wantExit)
	}
}

// selfHostMachOBytes compiles source with the self-host wasm emitter, runs the
// emitted module, and returns what the program wrote to stdout.
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

// machoDriver returns the wasm_run driver binary and the runner prefix needed
// to exec it. It skips when wasmtime is absent — every consumer runs the
// emitted module.
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
	copySelfHostDriver(t, dir, "wasm_run.fern")
	return runner, buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
}

var (
	darwinDriverOnce sync.Once
	darwinDriverPath string
	darwinDriverErr  error
)

// darwinMachoDriver builds wasm_run as a native arm64-darwin Mach-O, once per
// test binary — the build is ~13 s and eleven tests share it. The staging dir
// deliberately outlives any t.TempDir.
func darwinMachoDriver(t *testing.T) string {
	t.Helper()
	darwinDriverOnce.Do(func() {
		fern := buildLangBinForInterp(t)
		dir, err := os.MkdirTemp("", "selfhost-darwin-driver-")
		if err != nil {
			darwinDriverErr = err
			return
		}
		copySelfHostDriver(t, dir, "wasm_run.fern")
		bin := filepath.Join(dir, "wasm_run")
		out, err := exec.Command(fern, "-target", "arm64-darwin", "-o", bin,
			filepath.Join(dir, "wasm_run.fern")).CombinedOutput()
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
