package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/wasm/component"
)

var wasmBacktraceFrame = regexp.MustCompile(`<wasm function (\d+)>`)

// TestWASMHeapExhaustionTrapsInTheAllocator — a program that outgrows its
// linear memory has to fail at __fern_alloc, where the growth was refused.
//
// __fern_alloc used to drop memory.grow's result, so a refused grow read as
// success: the cursor advanced into memory that was never mapped and the
// program ran on until some unrelated store fell off the end. What wasmtime
// reported was "out of bounds memory access" inside whichever helper happened
// to write first — string concat here — with the allocator absent from the
// backtrace entirely (#6160).
func TestWASMHeapExhaustionTrapsInTheAllocator(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	// Doubling a string 40 times demands ~8 TB, so the 8 MiB cap below is
	// reached whatever the growth schedule underneath happens to be.
	const src = `function main(): i32 {
    var s: string = "abcdefgh";
    var i: i32 = 0;
    while (i < 40) {
        s = s + s;
        i = i + 1;
    }
    return s.len() as i32;
}`
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		ForceMemorySection: true,
		Preview2WASI:       true,
		SynthCliRun:        true,
	})
	if err != nil {
		t.Fatalf("wasmbin.Build: %v", err)
	}
	req, unsupported := component.ClassifyCore(core)
	if len(unsupported) > 0 {
		t.Fatalf("core module has imports the composer can't place: %v", unsupported)
	}
	comp := component.BuildWasiCliRunComponent(core, "_lang_run")
	if !component.RequestEmpty(req) {
		comp = component.Compose(core, req, "_lang_run")
	}
	compPath := filepath.Join(dir, "prog.component.wasm")
	if err := os.WriteFile(compPath, comp, 0o644); err != nil {
		t.Fatalf("write component: %v", err)
	}

	// The pooling allocator is what lets a memory be capped from the CLI;
	// 8 MiB is 128 pages.
	cmd := exec.Command("wasmtime", "run",
		"-O", "pooling-allocator=y",
		"-O", "pooling-max-memory-size=8388608",
		"-O", "pooling-total-memories=1",
		compPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("program fit inside an 8 MiB linear memory; the cap is not "+
			"biting, so this case proves nothing:\n%s", out)
	}
	report := string(out)
	if strings.Contains(report, "out of bounds memory access") {
		t.Fatalf("heap exhaustion still surfaces as an out-of-bounds access at "+
			"whichever store ran off the end, not at the allocator:\n%s", report)
	}
	if !strings.Contains(report, "wasm trap: wasm `unreachable` instruction executed") {
		t.Fatalf("want an `unreachable` trap from __fern_alloc:\n%s", report)
	}
	frames := wasmBacktraceFrame.FindAllStringSubmatch(report, -1)
	if len(frames) < 2 {
		t.Fatalf("want the allocator plus its callers in the backtrace, got %d "+
			"frame(s):\n%s", len(frames), report)
	}
	if frames[0][1] == frames[len(frames)-1][1] {
		t.Fatalf("innermost frame is the entry point itself, so the trap is not "+
			"attributable to the allocator:\n%s", report)
	}
}
