package e2eselfhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Native tcp_pollable is an identity; Wasm must subscribe to the socket and
// hand back a distinct owned pollable resource through the same scalar ABI.
func TestSelfHostWasmSemanticTCPPollable(t *testing.T) {
	for _, tool := range []string{"wasm-tools", "wasmtime"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	host := "x86-64-linux"
	if runtime.GOARCH == "arm64" {
		host = "arm64-" + runtime.GOOS
	}
	driver := filepath.Join(dir, "fern")
	if out, err := exec.Command(buildLangBinForInterp(t), "-target", host, "-o", driver, filepath.Join(dir, "fern.fern")).CombinedOutput(); err != nil {
		t.Fatalf("build compiler: %v\n%s", err, out)
	}
	src, wat := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wat")
	const probe = `function main(): i32 {
    var fd: i32 = tcp_listen(0);
    if (fd < 0) { return 1; }
    var p: i32 = tcp_pollable(fd);
    if (p < 0) { return 2; }
    if (wasm_pollable_drop(p) != 0) { return 3; }
    return tcp_close(fd);
}`
	if err := os.WriteFile(src, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(driver, "-target", "wasm32-wasi", "-emit", "asm", "-o", wat, src)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SEM_IR=1", "FERN_SEM_IR_REPORT=1")
	report, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, report)
	}
	requireCompleteHTTPSemanticLowering(t, report)
	text, err := os.ReadFile(wat)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "call $__fern_tcp_pollable") {
		t.Fatal("semantic tcp_pollable call was not emitted")
	}
	wit, err := filepath.Abs("../../cmd/fern/wit")
	if err != nil {
		t.Fatal(err)
	}
	core, embedded, component := filepath.Join(dir, "core.wasm"), filepath.Join(dir, "embedded.wasm"), filepath.Join(dir, "component.wasm")
	for _, args := range [][]string{
		{"parse", wat, "-o", core},
		{"component", "embed", wit, "-w", "fern", core, "-o", embedded},
		{"component", "new", embedded, "--adapt", "wasi_snapshot_preview1=" + adapter, "-o", component},
		{"validate", component},
	} {
		if out, err := exec.Command("wasm-tools", args...).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools %v: %v\n%s", args, err, out)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "wasmtime", "run", "-S", "inherit-network", component).CombinedOutput(); err != nil {
		t.Fatalf("live socket subscription: %v\n%s", err, out)
	}
}
