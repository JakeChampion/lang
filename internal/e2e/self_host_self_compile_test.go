package e2e

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostBootstrapsItself pipes a piece of the self-host
// source itself (lexer.lang, ~16 KB) through the asm-self-host
// driver and verifies the result assembles cleanly. This is the
// first checkpoint on the road to a real bootstrap.
//
// Known wall *before* attempting bigger inputs (parser.lang ~80 KB
// or asm.lang ~200 KB): the asm self-host's `s = s.out + text`
// pattern is O(N²) — for an N-byte output built via M writes, it
// allocates roughly N*M/2 total bytes through the bump heap which
// never reclaims. parser.lang would need ~7 GB; asm.lang ~60 GB.
// A real bootstrap needs either a growable-buffer primitive or a
// chunked-output `string[]` accumulator (the latter requires
// amortised-O(1) `array.push`, which today is O(N) per push).
func TestSelfHostBootstrapsItself(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.lang", "parser.lang", "asm.lang", "asm_run.lang"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	prog, _, err := modload.Load(filepath.Join(dir, "asm_run.lang"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverAsm := filepath.Join(dir, "driver.s")
	driverBin := filepath.Join(dir, "driver")
	if err := os.WriteFile(driverAsm, []byte(asm), 0o644); err != nil {
		t.Fatalf("write driver asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

	// Read the lexer.lang source (smaller — 442 lines vs asm.lang's
	// 6391) and pipe it into the driver. Used as a baseline probe.
	asmLangPath := filepath.Join("../../examples/self_host", "lexer.lang")
	asmLangSrc, err := os.ReadFile(asmLangPath)
	if err != nil {
		t.Fatalf("read lexer.lang: %v", err)
	}
	t.Logf("piping all %d bytes of lexer.lang", len(asmLangSrc))

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
	}
	cmd.Stdin = bytes.NewReader(asmLangSrc)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start driver: %v", err)
	}
	emittedAsm, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	werr := cmd.Wait()

	emittedPath := filepath.Join(dir, "self_hosted_asm.s")
	if err := os.WriteFile(emittedPath, emittedAsm, 0o644); err != nil {
		t.Fatalf("write emitted: %v", err)
	}

	// First sanity check: did the driver finish?
	if werr != nil {
		t.Fatalf("driver wait err: %v\nstderr:\n%s\nemitted bytes: %d (at %s)",
			werr, stderr.String(), len(emittedAsm), emittedPath)
	}
	if len(emittedAsm) == 0 {
		t.Fatalf("driver emitted 0 bytes; stderr:\n%s", stderr.String())
	}

	// Second sanity check: can gcc assemble it?
	// Copy emitted asm to a stable path so we can inspect after fail.
	copyPath := "/tmp/last_self_hosted.s"
	_ = os.WriteFile(copyPath, emittedAsm, 0o644)
	innerBin := filepath.Join(dir, "self_hosted")
	out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", emittedPath, "-o", innerBin).CombinedOutput()
	if err != nil {
		t.Fatalf("gcc on emitted asm: %v\n%s\nemitted asm saved to %s",
			err, out, copyPath)
	}
	t.Logf("self-host bootstrap probe: emitted %d bytes, gcc-assembled OK -> %s",
		len(emittedAsm), innerBin)
}
