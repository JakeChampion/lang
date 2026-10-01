package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Deploy-time configuration (#9855): `plat.config` and `plat.secret` read
// the environment on a target that has one.
func TestPlatformConfigFromEnvX86_64(t *testing.T) {
	port := freeLoopbackPort(t)
	bin, runner := buildSupervisedServeBin(t, e2eharness.ConfigHandlerSource())
	startSupervisedServer(t, bin, runner, append([]string{fmt.Sprintf("PORT=%d", port)}, e2eharness.ConfigHandlerEnv...)...)
	e2eharness.CheckConfigHandler(t, fmt.Sprintf("127.0.0.1:%d", port))
}

// The proxy world has no environment: the same handler, built for
// wasm32-wasi-http, reads wasi:config/store, which `wasmtime serve` fills
// from its config-var flags.
func TestPlatformConfigFromWasiConfig(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Fatalf("wasmtime not on PATH: %v", err)
	}
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "handler.fern")
	if err := os.WriteFile(src, []byte(e2eharness.ConfigHandlerSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	component := filepath.Join(dir, "handler.wasm")
	if out, err := exec.Command(fern, "-target", "wasm32-wasi-http", "-o", component, src).CombinedOutput(); err != nil {
		t.Fatalf("fern -target wasm32-wasi-http: %v\n%s", err, out)
	}
	e2eharness.CheckConfigHandler(t, e2eharness.ServeComponentWithConfig(t, wasmtime, component))
}
