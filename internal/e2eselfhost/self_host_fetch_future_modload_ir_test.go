package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostFetchFutureModloadIRX86_64 is the capstone of async slice 6c: the
// real std/fetch `fetch_future` — whose `__fetch_drain` returns
// `Pending(tcp_pollable(c), resume)` where the nested `resume` captures the
// connection fd (i32) AND a string accumulator — now compiles through the
// self-host MODLOAD IR path instead of bailing to AST. It pulls together every
// prior 6c slice: the function-typed enum payload (5/5b), value-used nested
// fn-values (6c parts 1+2), and pointer-shaped captures in the env box (6c part
// 3 — the string accumulator capture).
//
// The program is compiled via the asm_load_run modload driver and asserted to
// route the IR path (-decide == "ir"). It is NOT run here: fetch_future performs
// real TCP I/O (connect to a host:port), which needs a live server; the IR-vs-AST
// routing — the goal-1 objective (never take the AST fallback) — is what this
// guards. The capture behaviour itself is run end-to-end by
// TestSelfHostClosurePtrCaptureIRX86_64.
func TestSelfHostFetchFutureModloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
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
import "std/fetch";
function main(): i32 {
    var host: i32 = 127 | (1 << 24);
    var f: async.Future[string] = fetch.fetch_future(host, 8080, "/");
    var fs: async.Future[string][] = [f];
    var bodies: string[] = async.gather(fs, "");
    return bodies[0].len();
}
`
	proj := t.TempDir()
	mainPath := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(mainPath, []byte(prog), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	decide, err := exec.Command(mmc, mainPath, stdlibRoot, "-decide").Output()
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if got := strings.TrimSpace(string(decide)); got != "ir" {
		t.Fatalf("fetch_future routed %q, want \"ir\" (pointer-capture closure bailed to AST)", got)
	}
	asm, err := exec.Command(mmc, mainPath, stdlibRoot).Output()
	if err != nil {
		t.Fatalf("loader compile: %v", err)
	}
	if len(asm) == 0 {
		t.Fatal("loader emitted 0 bytes")
	}
}
