package e2ecompiler

import (
	"fmt"
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostPlatformConfigFromEnv(t *testing.T) {
	port := selfHostFreePort(t)
	bin, runner := selfHostServer(t, e2eharness.ConfigHandlerSource())
	cmd := binCmd(runner, bin)
	cmd.Env = append(append(cmd.Environ(), fmt.Sprintf("PORT=%d", port)), e2eharness.ConfigHandlerEnv...)
	e2eharness.StartServerProcess(t, cmd)
	e2eharness.CheckConfigHandler(t, fmt.Sprintf("127.0.0.1:%d", port))
}

func TestSelfHostPlatformConfigFromWasiConfig(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Fatalf("wasmtime not on PATH: %v", err)
	}
	component := compileWasiHttp(t, t.TempDir(), e2eharness.ConfigHandlerSource(), "handler.wasm")
	e2eharness.CheckConfigHandler(t, e2eharness.ServeComponentWithConfig(t, wasmtime, component))
}
