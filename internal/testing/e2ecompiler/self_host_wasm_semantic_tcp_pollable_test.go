package e2ecompiler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	e2eharness "github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostWasmSemanticTCPPollable(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
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
    let fd: i32 = tcp_listen(0);
    if (fd < 0) { return 1; }
    let p: i32 = tcp_pollable(fd);
    if (p < 0) { return 2; }
    if (wasm_pollable_drop(p) != 0) { return 3; }
    return tcp_close(fd);
}`
	if err := os.WriteFile(src, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(driver, "-target", "wasm32-wasi", "-emit", "asm", "-o", wat, src)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SEM_IR_REPORT=1")
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
	component := filepath.Join(dir, "main.wasm")
	if out, err := exec.Command(driver, "-target", "wasm32-wasi", "-o", component, src).CombinedOutput(); err != nil {
		t.Fatalf("compile to a component: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, wasmtime, "run", "-S", "inherit-network", component).CombinedOutput(); err != nil {
		t.Fatalf("live socket subscription: %v\n%s", err, out)
	}
}
