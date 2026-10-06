package e2ecompiler

import (
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostPlatformConfigFromEnv(t *testing.T) {
	bin, runner := selfHostServer(t, e2eharness.ConfigHandlerSource())
	cmd := binCmd(runner, bin)
	cmd.Env = append(cmd.Environ(), e2eharness.ConfigHandlerEnv...)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	e2eharness.CheckConfigHandler(t, addr)
}

func TestSelfHostPlatformConfigFromWasiConfig(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Fatalf("wasmtime not on PATH: %v", err)
	}
	component := compileWasiHttp(t, t.TempDir(), e2eharness.ConfigHandlerSource(), "handler.wasm")
	e2eharness.CheckConfigHandler(t, e2eharness.ServeComponentWithConfig(t, wasmtime, component))
}
