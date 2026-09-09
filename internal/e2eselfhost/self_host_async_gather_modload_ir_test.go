package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostAsyncGatherModloadIRX86_64 is slice 6 of docs/ASYNC-SELFHOST-IR.md
// — the end-to-end payoff: a self-host-compiled `std/async` program (gather over
// a Future[i32][]) compiles through the MODLOAD driver's IR path and runs.
//
// This exercises the full stack landed across slices 1-5b (poll, the Future
// enum's function-typed/closure payloads) PLUS the slice-6 flatten fix: an
// imported generic enum (`async.Future[T]`) used inside an imported generic
// function (`async.gather`) now monomorphizes correctly, because flatten now
// mangles the imported EnumDecls + variant-struct enum_owner to match the
// mangled variant structs. An EnumDecl that keeps bare names leaves
// monomorphize_enums unable to find the mangled variants, bailing the whole
// merged program (and the AST emitter it fell to could not emit poll or the
// Future constructor at all).
//
// The driver's `-decide` must report `ir` (the merged program routes the IR
// path), and the compiled binary must match the interpreter oracle
// (sum of three Ready values = 42). The configured runner executes the driver
// and output binary with the same filesystem paths.
func TestSelfHostAsyncGatherModloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	prog := `import "std/async";

function main(): i32 {
    var fs: async.Future[i32][] = [Ready(5), Ready(7), Ready(30)];
    var summed: i32[] = async.gather(fs, -1);
    return summed[0] + summed[1] + summed[2];
}
`
	want := interpExit(t, interpBin, prog) // 42

	proj := t.TempDir()
	mainPath := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(mainPath, []byte(prog), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}

	// (1) The merged multi-module program must route the IR path, not AST.
	decide, err := runX86_64Bin(runner, mmc, mainPath, stdlibRoot, "-decide").CombinedOutput()
	if err != nil {
		t.Fatalf("decide: %v\n%s", err, decide)
	}
	if got := strings.TrimSpace(string(decide)); got != "ir" {
		t.Fatalf("gather/std/async routed %q, want \"ir\" (imported generic enum bailed)", got)
	}

	// (2) It compiles + runs to the interpreter oracle (42).
	asm, err := runX86_64Bin(runner, mmc, mainPath, stdlibRoot).CombinedOutput()
	if err != nil {
		t.Fatalf("loader compile: %v\n%s", err, asm)
	}
	if len(asm) == 0 {
		t.Fatal("loader emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "async_gather", string(asm))
	cmd := runX86_64Bin(runner, progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("gather exited %d, want %d (interp oracle)", code, want)
	}
}
