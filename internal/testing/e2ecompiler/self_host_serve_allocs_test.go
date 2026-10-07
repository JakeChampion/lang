package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// #9853 exit criterion A, count half: what the serve loop allocates per
// hello request on a keep-alive connection, through the self-host compiler.
// docs/NET-P0-MESSAGE-LAYER-PLAN.md §6 took it to zero on x86-64 over
// 2,000 requests; the gate spans 100k there and holds the same zero on
// arm64 under qemu and on wasm under wasmtime over 10k each.
func TestSelfHostServeAllocsPerRequest(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsServerSource(0, e2eharness.ServeAllocsRounds)), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
	cmd := runX86_64Bin(cli.runner, bin)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckServeAllocs(t, addr, 0, e2eharness.ServeAllocsRounds)
}

func TestSelfHostServeAllocsPerRequestArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsServerSource(0, e2eharness.ServeAllocsRoundsEmulated)), 0o644); err != nil {
		t.Fatal(err)
	}
	addr, _ := e2eharness.StartInheritedServer(t, runArm64Bin(qemu, cli.arm64Binary(t, src, "FERN_STRICT_IR=1")))
	e2eharness.CheckServeAllocs(t, addr, 0, e2eharness.ServeAllocsRoundsEmulated)
}

// The wasm leg binds a port the test picked, since wasmtime hands a guest
// no inherited listener, and runs under `-S inherit-network`.
func TestSelfHostServeAllocsPerRequestWasm(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	cli := buildSelfHostCLI(t)
	port := freeTCPPort(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ServeAllocsServerSource(port, e2eharness.ServeAllocsRoundsEmulated)), 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command(wasmtime, "run", "-S", "inherit-network", cli.wasmComponent(t, src, "FERN_STRICT_IR=1"))
	var out bytes.Buffer
	run.Stdout, run.Stderr = &out, &out
	if err := run.Start(); err != nil {
		t.Fatalf("wasmtime run: %v", err)
	}
	t.Cleanup(func() {
		_ = run.Process.Kill()
		_ = run.Wait()
		if t.Failed() {
			t.Logf("server output:\n%s", out.String())
		}
	})
	e2eharness.CheckServeAllocs(t, fmt.Sprintf("127.0.0.1:%d", port), 0, e2eharness.ServeAllocsRoundsEmulated)
}
