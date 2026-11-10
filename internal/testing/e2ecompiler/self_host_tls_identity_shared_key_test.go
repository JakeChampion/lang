package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostTLSIdentitySharedKeyCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := e2eharness.RepoPath("tests/stdlib/tls_identity_shared_key_test.fern")
	for _, target := range []string{"x86-64", "wasm"} {
		t.Run(target, func(t *testing.T) {
			var cmd *exec.Cmd
			if target == "wasm" {
				cmd = exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, source, "FERN_LEAKCHECK=1"))
			} else {
				cmd = runX86_64Bin(cli.runner, cli.x86Binary(t, source, "FERN_LEAKCHECK=1"))
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("identity sharing: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), "# pass 2\n# fail 0\n") {
				t.Fatalf("identity sharing: %s", stdout.String())
			}
			assertBalancedCensus(t, stderr.String())
		})
	}
}
