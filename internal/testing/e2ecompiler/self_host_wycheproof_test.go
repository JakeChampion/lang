package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The ECDSA Wycheproof suite runs natively alone. The oracle interprets the
// P-256 arithmetic and takes about 25 minutes on its 484 vectors, so the
// std test gate, which runs every suite under the oracle too, leaves it out.
func TestSelfHostWycheproofEcdsaP256(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	src := langSrcAbs(t, "tests/stdlib/wycheproof_ecdsa_p256_test.fern")
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "suite")
			if out, err := cli.compileCmd(target, bin, src).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			if err := os.Chmod(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			var cmd *exec.Cmd
			if target == "arm64-linux" {
				cmd = runArm64Bin(qemu, bin)
			} else {
				cmd = runX86_64Bin(cli.runner, bin)
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			_ = cmd.Run()
			code := cmd.ProcessState.ExitCode()
			if code != 0 || !strings.Contains(stdout.String(), "\n# fail 0\n") || strings.Contains(stdout.String(), "\nnot ok") {
				t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
			}
		})
	}
}
