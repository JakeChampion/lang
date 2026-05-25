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

// TestSelfHostBootstrapsItself pipes self-host source files through
// the asm-self-host driver and verifies each result assembles +
// links cleanly. The driver now compiles its own COMPLETE source —
// lexer.fern (~40 KB), parser.fern (~86 KB), and asm.fern (~294 KB,
// the x86-64 emitter itself) — to gcc-assemblable, linkable asm.
//
// Three classes of wall used to block the bigger inputs, all fixed:
//   1. The asm self-host's `s = s.out + text` output build was O(N²)
//      — replaced on both backends by the amortised-O(1) global
//      strbuf primitive (strbuf_reset / strbuf_append / strbuf_take);
//      array.push is likewise amortised O(1) (geometric push-grow
//      with an in-place rc==1 fast path).
//   2. A family of parser non-advance runaways: parse_type_name and
//      parse_pattern read only a single base identifier, so a
//      qualified type name (`lexer.Token`) or qualified variant
//      pattern (`lexer.TokNumber(n)`) left a stray `.` on the cursor
//      and the surrounding loop spun, allocating until OOM. Since
//      parser.fern is itself full of `lexer.*` qualified types and
//      patterns, this blocked it from parsing its own source.
//   3. The self-host emitter didn't implement the strbuf builtins, so
//      a program that USES strbuf — asm.fern's own `write` does —
//      linked with undefined `__fern_strbuf_*` references. The
//      emitter now emits the strbuf runtime (a 16-byte-box string
//      ABI) alongside the heap, so asm.fern self-compiles + links.
//
// The gate here is assemble + link, not execution — same bar the
// lexer / parser probes are held to (their outputs aren't run
// either). Running the self-compiled emitter (a true stage-2
// bootstrap) is the next frontier: the linked asm.fern binary still
// faults at runtime (heap sizing + emit-correctness), to be chased
// in follow-ups.
func TestSelfHostBootstrapsItself(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "asm.fern", "asm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	prog, _, err := modload.Load(filepath.Join(dir, "asm_run.fern"))
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

	// Pipe self-host source files through the driver and assert each
	// emits gcc-assemblable asm. lexer.fern (~40 KB) is the baseline
	// probe; parser.fern (~86 KB) was historically OOM-killed by the
	// O(N²) `s.out + text` output build AND a family of parser
	// non-advance runaways (qualified type names / qualified variant
	// patterns) — all since fixed, so the self-host compiler now
	// compiles its own lexer AND parser. asm.fern is NOT yet in this
	// list: it emits without OOM but its output doesn't assemble
	// cleanly yet (a separate emit-correctness gap — follow-up).
	for _, name := range []string{"lexer.fern", "parser.fern", "asm.fern"} {
		langSrc, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		t.Logf("piping all %d bytes of %s", len(langSrc), name)

		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
		}
		cmd.Stdin = bytes.NewReader(langSrc)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("%s: stdout pipe: %v", name, err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatalf("%s: start driver: %v", name, err)
		}
		emittedAsm, err := io.ReadAll(stdout)
		if err != nil {
			t.Fatalf("%s: read stdout: %v", name, err)
		}
		werr := cmd.Wait()

		emittedPath := filepath.Join(dir, name+".self_hosted.s")
		if err := os.WriteFile(emittedPath, emittedAsm, 0o644); err != nil {
			t.Fatalf("%s: write emitted: %v", name, err)
		}

		// First sanity check: did the driver finish?
		if werr != nil {
			t.Fatalf("%s: driver wait err: %v\nstderr:\n%s\nemitted bytes: %d (at %s)",
				name, werr, stderr.String(), len(emittedAsm), emittedPath)
		}
		if len(emittedAsm) == 0 {
			t.Fatalf("%s: driver emitted 0 bytes; stderr:\n%s", name, stderr.String())
		}

		// Second sanity check: can gcc assemble it?
		innerBin := filepath.Join(dir, name+".self_hosted")
		out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", emittedPath, "-o", innerBin).CombinedOutput()
		if err != nil {
			copyPath := filepath.Join("/tmp", name+".last_self_hosted.s")
			_ = os.WriteFile(copyPath, emittedAsm, 0o644)
			t.Fatalf("%s: gcc on emitted asm: %v\n%s\nemitted asm saved to %s",
				name, err, out, copyPath)
		}
		t.Logf("self-host bootstrap probe: %s -> %d bytes asm, gcc-assembled OK -> %s",
			name, len(emittedAsm), innerBin)
	}
}
