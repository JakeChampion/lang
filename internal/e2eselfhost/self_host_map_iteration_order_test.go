package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The map iteration-order gate (#8839). `Map[K, V]` iterates in INSERTION
// order — `core/map` states that contract and `std/json` rides it to preserve
// an object's key order — and until this gate existed nothing checked that the
// self-host's backends agreed with native about it. Two of them did not, in
// different ways, and both were silent: the program runs, prints, and exits 0.
//
//   - The register backends (x86-64 / arm64, sharing asmcore's runtime source)
//     deleted by SHIFTING the tail down, where core/map moves the LAST entry
//     into the hole. Same set of keys either way, different sequence, and only
//     after a `.without`.
//   - The wasm backend kept keys at their probe slot and snapshotted by
//     sweeping the slot table, so its `keys()` came out in HASH order. That one
//     diverges on three plain inserts with no delete in sight.
//
// The oracle is the interpreter, as everywhere else here: native is the
// reference implementation, and the compilers must agree with it rather than
// with each other.
//
// Twelve inserts are the load-bearing number: the map starts at capacity 8 and
// grows at 3/4, so the sequence has to survive a rehash — which is exactly
// where an order column can be rebuilt in the wrong sequence and where a slot
// sweep looks correct by accident on a small map.
const mapIterationOrderSrc = `import "core/map";
function main(): i32 {
    var m: Map[i32, i32] = Map {};
    var i: i32 = 0;
    while (i < 12) { m = m.insert(i, i * 2); i = i + 1; }
    var ks: i32[] = m.keys();
    if (ks.len() != 12) { return 10; }
    var j: i32 = 0;
    while (j < 12) { if (ks[j] != j) { return 20 + j; } j = j + 1; }
    var vs: i32[] = m.values();
    if (vs[5] != 10) { return 34; }
    m = m.insert(3, 99);
    ks = m.keys();
    if (ks[3] != 3) { return 40; }
    var d: (Map[i32, i32], boolean) = m.without(1);
    m = d.0;
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

// Each code says which property broke, so a failure needs no bisect:
//
//	10        keys() lost or gained an entry
//	20+j      the fresh inserts do not read back in insertion order — j is the
//	          first position that disagrees, so 21 is "the second key is wrong"
//	          and points at hash order rather than at the grow
//	34        values() disagrees with keys() about the sequence
//	40        an overwrite MOVED its key instead of keeping its position
//	50 / 51   `.without` did not move the last entry into the hole
//	52        it moved something else as well
//	60 / 61   an insert after a delete did not append at the end
//	42        every property holds
//
// 51 is what the register backends answered before this gate; 21 is what wasm
// answered.

// mapIterationOrderOracle is InterpExit with a stdlib root, which it does not
// take. The program needs one: native REQUIRES `import "core/map"` for a map
// operation (E001) where the self-host lowers one without it.
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

// mapIterationOrderProject builds the self-host `fern` driver once per leg.
// fern.fern rather than asm_ir_run.fern because the program imports core/map,
// which needs the module loader and a stdlib root.
func mapIterationOrderProject(t *testing.T, gcc string) (fernBin, stdlibRoot, mainPath string) {
	t.Helper()
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin = buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	root, err := filepath.Abs("../../internal/stdlib")
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
	if out, err := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", mainPath, stdlibRoot, "-o", asmPath).CombinedOutput(); err != nil {
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

// The arm64 leg shares asmcore's `__fern_map_delete` source with x86-64, so it
// cannot diverge from it on the delete — but it reaches that source through its
// own emitter, and the array-box addressing the swap uses is per-backend.
func TestSelfHostMapIterationOrderArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	fernBin, stdlibRoot, mainPath := mapIterationOrderProject(t, x86gcc)
	want := mapIterationOrderOracle(t, interpBin, stdlibRoot)

	proj := t.TempDir()
	asmPath := filepath.Join(proj, "out.s")
	if out, err := runX86_64Bin(x86runner, fernBin, "-target", "arm64-linux", "-emit", "asm", mainPath, stdlibRoot, "-o", asmPath).CombinedOutput(); err != nil {
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

// The wasm leg is the one that carries a distinct implementation: its map is an
// open-addressed table with tombstones rather than asmcore's two columns, so
// the order column and the three sites that maintain it (set, delete, grow) are
// wasm-only code. It is also the leg that was wrong without any delete at all.
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
	cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", mainPath, stdlibRoot, "-o", outWat)
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
