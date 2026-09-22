package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostWasmTcpLifecycleCensus(t *testing.T) {
	for _, tool := range []string{"wasm-tools", "wasmtime"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	cmd := runX86_64Bin(runner, bin)
	cmd.Env = append(os.Environ(), "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
	cmd.Stdin = strings.NewReader(e2eharness.WasiTCPCensusProbe)
	wat, err := cmd.Output()
	if err != nil {
		t.Fatalf("self-host TCP census: %v", err)
	}
	path := filepath.Join(dir, "census.wat")
	if err := os.WriteFile(path, wat, 0o644); err != nil {
		t.Fatal(err)
	}
	wit, err := filepath.Abs("../../cmd/fern/wit")
	if err != nil {
		t.Fatal(err)
	}
	core, embedded, component := filepath.Join(dir, "core.wasm"), filepath.Join(dir, "embedded.wasm"), filepath.Join(dir, "component.wasm")
	for _, args := range [][]string{
		{"parse", path, "-o", core},
		{"component", "embed", wit, "-w", "fern", core, "-o", embedded},
		{"component", "new", embedded, "--adapt", "wasi_snapshot_preview1=" + adapter, "-o", component},
		{"validate", component},
	} {
		if out, err := exec.Command("wasm-tools", args...).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools %v: %v\n%s", args, err, out)
		}
	}
	e2eharness.CheckWasiTCPCensus(t, component)
}

func TestSelfHostWasmSocketSetupReclaimsOnError(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	for _, tc := range []struct{ name, expr string }{
		{"listen", "tcp_listen(0)"},
		{"connect", "tcp_connect(0, 1)"},
		{"udp", "udp_send(\"127.0.0.1\", 1, \"x\")"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runX86_64Bin(runner, bin)
			cmd.Stdin = strings.NewReader("function main(): i32 { return " + tc.expr + "; }")
			wat, err := cmd.Output()
			if err != nil {
				t.Fatalf("self-host socket probe: %v", err)
			}
			path := filepath.Join(t.TempDir(), "socket.wat")
			if err := os.WriteFile(path, wat, 0o644); err != nil {
				t.Fatal(err)
			}
			e2eharness.CheckWasiSocketReclaim(t, path, tc.name)
		})
	}
}

func TestSelfHostWasmSocketCloseZeroHandles(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	for _, tc := range []struct{ name, expr string }{
		{"listen", "tcp_listen(0)"},
		{"connect", "tcp_connect(0, 1)"},
		{"accept", "tcp_accept(0)"}, // harness supplies a borrowed listener record
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runX86_64Bin(runner, bin)
			cmd.Stdin = strings.NewReader("function main(): i32 { var h: i32 = " + tc.expr + "; if (h < 0) { return h; } return tcp_close(h); }")
			wat, err := cmd.Output()
			if err != nil {
				t.Fatalf("self-host socket probe: %v", err)
			}
			path := filepath.Join(t.TempDir(), "socket.wat")
			if err := os.WriteFile(path, wat, 0o644); err != nil {
				t.Fatal(err)
			}
			e2eharness.CheckWasiSocketReclaim(t, path, tc.name+"-close")
		})
	}
}

func TestSelfHostWasmSocketGuestStorage(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	t.Run("close-only-heap-dependency", func(t *testing.T) {
		cmd := runX86_64Bin(runner, bin)
		cmd.Stdin = strings.NewReader("function main(): i32 { return tcp_close(0); }")
		wat, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "close.wat")
		if err := os.WriteFile(path, wat, 0o644); err != nil {
			t.Fatal(err)
		}
		// Validate without executing an invalid connection. Close must bring
		// in its allocator return path without a constructor in this module.
		if out, err := exec.Command("wasm-tools", "validate", path).CombinedOutput(); err != nil {
			t.Fatalf("close-only module: %v\n%s", err, out)
		}
	})
	for _, tc := range []struct{ name, expr string }{
		{"listen", "tcp_listen(0)"},
		{"connect", "tcp_connect(0, 1)"},
		{"accept", "tcp_accept(0)"}, // harness supplies a borrowed listener record
		{"port", "tcp_local_port(0)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runX86_64Bin(runner, bin)
			cmd.Stdin = strings.NewReader(e2eharness.WasiSocketStorageProbe(tc.expr, tc.name != "port"))
			wat, err := cmd.Output()
			if err != nil {
				t.Fatalf("self-host socket probe: %v", err)
			}
			path := filepath.Join(t.TempDir(), "socket.wat")
			if err := os.WriteFile(path, wat, 0o644); err != nil {
				t.Fatal(err)
			}
			operation := tc.name
			if operation != "port" {
				operation += "-close"
			}
			e2eharness.CheckWasiSocketReclaim(t, path, operation)
		})
	}
}
