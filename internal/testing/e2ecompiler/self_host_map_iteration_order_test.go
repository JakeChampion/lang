package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const mapIterationOrderSrc = `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = Map {};
    let i: i32 = 0;
    while (i < 12) { m = m.insert(i, i * 2); i = i + 1; }
    let ks: i32[] = m.keys();
    if (ks.len() != 12) { return 10; }
    let j: i32 = 0;
    while (j < 12) { if (ks[j] != j) { return 20 + j; } j = j + 1; }
    let vs: i32[] = m.values();
    if (vs[5] != 10) { return 34; }
    m = m.insert(3, 99);
    ks = m.keys();
    if (ks[3] != 3) { return 40; }
    let d: Map[i32, i32] = m.remove(1);
    m = d;
    ks = m.keys();
    if (ks.len() != 11) { return 50; }
    if (ks[1] != 11) { return 51; }
    if (ks[10] != 10) { return 52; }
    m = m.insert(77, 7);
    ks = m.keys();
    if (ks.len() != 12) { return 60; }
    if (ks[11] != 77) { return 61; }
    return 42;
}
`

func mapIterationOrderOracle(t *testing.T, interpBin, stdlibRoot string) int {
	t.Helper()
	f := filepath.Join(t.TempDir(), "oracle.fern")
	if err := os.WriteFile(f, []byte(mapIterationOrderSrc), 0o644); err != nil {
		t.Fatalf("write oracle src: %v", err)
	}
	cmd := exec.Command(interpBin, "-interp", f, stdlibRoot)
	var errb strings.Builder
	cmd.Stderr = &errb
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("interp did not run\nstderr: %s", errb.String())
	}
	got := cmd.ProcessState.ExitCode()
	if got != 42 {
		t.Fatalf("the ORACLE answered %d, not 42 — native's own map iteration order moved, "+
			"so this gate is measuring against a changed reference\nstderr: %s", got, errb.String())
	}
	return got
}

func mapIterationOrderProject(t *testing.T, gcc string) (fernBin, stdlibRoot, mainPath string) {
	t.Helper()
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin = buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	mainPath = filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(mainPath, []byte(mapIterationOrderSrc), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	return fernBin, root, mainPath
}

func TestSelfHostMapIterationOrderX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	fernBin, stdlibRoot, mainPath := mapIterationOrderProject(t, gcc)
	want := mapIterationOrderOracle(t, interpBin, stdlibRoot)

	proj := t.TempDir()
	asmPath := filepath.Join(proj, "out.s")
	if out, err := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, mainPath, stdlibRoot).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v (%s)", err, out)
	}
	binPath := filepath.Join(proj, "out.bin")
	if out, err := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); err != nil {
		t.Fatalf("link: %v (%s)", err, out)
	}
	cmd := runX86_64Bin(runner, binPath)
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != want {
		t.Errorf("x86-64 = %d, want %d (interp oracle) — see the code table above", got, want)
	}
}

func TestSelfHostMapIterationOrderArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	fernBin, stdlibRoot, mainPath := mapIterationOrderProject(t, x86gcc)
	want := mapIterationOrderOracle(t, interpBin, stdlibRoot)

	proj := t.TempDir()
	asmPath := filepath.Join(proj, "out.s")
	if out, err := runX86_64Bin(x86runner, fernBin, "-target", "arm64-linux", "-emit", "asm", "-o", asmPath, mainPath, stdlibRoot).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v (%s)", err, out)
	}
	asm, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatalf("read asm: %v", err)
	}
	bin := buildBinArm64(t, arm64gcc, proj, "map_order_arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if got := cmd.ProcessState.ExitCode(); got != want {
		t.Errorf("arm64 = %d, want %d (interp oracle) — see the code table above", got, want)
	}
}

func TestSelfHostMapIterationOrderWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the wasm map-iteration-order leg")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	fernBin, stdlibRoot, mainPath := mapIterationOrderProject(t, gcc)
	want := mapIterationOrderOracle(t, interpBin, stdlibRoot)

	proj := t.TempDir()
	outWat := filepath.Join(proj, "out.wat")
	var stderr strings.Builder
	cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", outWat, mainPath, stdlibRoot)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("compile: %v (%s)", err, stderr.String())
	}
	rcmd := exec.Command("wasmtime", "run", outWat)
	_ = rcmd.Run()
	if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
		t.Fatal("wasmtime did not exit normally")
	}
	if got := rcmd.ProcessState.ExitCode(); got != want {
		t.Errorf("wasm = %d, want %d (interp oracle) — see the code table above", got, want)
	}
}
