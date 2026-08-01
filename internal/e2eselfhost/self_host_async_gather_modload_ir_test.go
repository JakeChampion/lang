package e2eselfhost

import (
	"os"
	"os/exec"
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
// mangled variant structs (previously the EnumDecl kept bare names, so
// monomorphize_enums couldn't find the mangled variants and the whole merged
// program bailed to the AST emitter — where poll / the Future constructor can't
// be emitted at all).
//
// The driver's `-decide` must report `ir` (the merged program routes the IR
// path, not AST), and the compiled binary must match the interpreter oracle
// (sum of three Ready values = 42). x86-64 only (the loader driver takes argv
// file paths, so it can't run under the qemu runner — mirrors
// TestSelfHostStdlibModloadIRX86_64).
func TestSelfHostAsyncGatherModloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "flatten.fern", "checker.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "treeshake.fern", "asm_arm64_ir.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
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
	decide, err := exec.Command(mmc, mainPath, stdlibRoot, "-decide").Output()
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if got := strings.TrimSpace(string(decide)); got != "ir" {
		t.Fatalf("gather/std/async routed %q, want \"ir\" (imported generic enum bailed to AST)", got)
	}

	// (2) It compiles + runs to the interpreter oracle (42).
	asm, err := exec.Command(mmc, mainPath, stdlibRoot).Output()
	if err != nil {
		t.Fatalf("loader compile: %v", err)
	}
	if len(asm) == 0 {
		t.Fatal("loader emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "async_gather", string(asm))
	cmd := exec.Command(progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != want {
		t.Errorf("gather exited %d, want %d (interp oracle)", code, want)
	}
}
