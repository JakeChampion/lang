package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostWasmIRLargeFreelist(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR large-freelist e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping wasm-IR large-freelist e2e")
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset; skipping wasm-IR large-freelist e2e")
	}
	if _, err := os.Stat(adapter); err != nil {
		t.Skipf("adapter %s not found; skipping", adapter)
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "wasm_ir_run")
	witDir, err := filepath.Abs("../../../cmd/fern/wit")
	if err != nil {
		t.Fatalf("abs wit dir: %v", err)
	}

	src := func(iters int) string {
		return fmt.Sprintf(`function fill(n: i32): i32[] {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < n) { a = a.append(i); i = i + 1; }
    return a;
}
function main(): i32 {
    let r: i32 = 0;
    let i: i32 = 0;
    while (i < %d) {
        let a: i32[] = fill(160000);
        r = r + a.len();
        i = i + 1;
    }
    write("kib=");
    print_int((__heap_bump_bytes() / 1024i64) as i32);
    write("\n");
    return 0;
}`, iters)
	}

	bumpKiB := func(name string, iters int) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(withPrintInt(src(iters))))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("%s: wasm_ir_run failed: %v", name, err)
		}
		if !strings.Contains(string(wat), "$__fern_large_push") {
			t.Fatalf("%s: emitted WAT has no $__fern_large_push", name)
		}
		watPath := filepath.Join(dir, name+".wat")
		if err := os.WriteFile(watPath, wat, 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		corePath := filepath.Join(dir, name+".core.wasm")
		if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools parse: %v\n%s", err, out)
		}
		embedPath := filepath.Join(dir, name+".embed.wasm")
		if out, err := exec.Command(wasmtools, "component", "embed", witDir,
			"-w", "fern", corePath, "-o", embedPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools component embed: %v\n%s", err, out)
		}
		compPath := filepath.Join(dir, name+".component.wasm")
		if out, err := exec.Command(wasmtools, "component", "new", embedPath,
			"--adapt", "wasi_snapshot_preview1="+adapter, "-o", compPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools component new: %v\n%s", err, out)
		}
		out, err := exec.Command(wasmtime, "run", compPath).Output()
		if err != nil {
			t.Fatalf("%s: wasmtime run: %v", name, err)
		}
		line := strings.TrimSpace(string(out))
		kib, perr := strconv.Atoi(strings.TrimPrefix(line, "kib="))
		if !strings.HasPrefix(line, "kib=") || perr != nil {
			t.Fatalf("%s: unparsable bump report %q", name, line)
		}
		return kib
	}

	const blockKiB = 1536
	small := bumpKiB("iters2", 2)
	large := bumpKiB("iters8", 8)

	if growth := large - small; growth >= blockKiB {
		t.Errorf("heap bump grew %d KiB over six extra iterations (2 iters: %d KiB, 8 iters: %d KiB); "+
			"a large block is not returning to the freelist",
			growth, small, large)
	}
}
