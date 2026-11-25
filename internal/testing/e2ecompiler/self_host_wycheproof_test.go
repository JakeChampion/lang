package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run the larger Wycheproof suites on the native targets. The oracle alone
// takes about 25 minutes for the P-256 signature vectors.
func TestSelfHostWycheproofNative(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, suite := range []string{
		"ecdh_p256",
		"ecdsa_p256",
		"ecdsa_p256_sha512",
		"ecdsa_p384_sha384",
		"hkdf_sha256",
		"hmac_sha256",
		"mlkem768",
		"mlkem768_encaps",
		"mlkem768_keygen",
		"rsa_oaep",
		"rsa_pkcs1v15",
		"rsa_pss",
		"xchacha20poly1305",
	} {
		src := langSrcAbs(t, "tests/stdlib/wycheproof_"+suite+"_test.fern")
		for _, target := range []string{"x86-64-linux", "arm64-linux"} {
			t.Run(suite+"/"+target, func(t *testing.T) {
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
				err := cmd.Run()
				if err != nil || !strings.Contains(stdout.String(), "\n# fail 0\n") || strings.Contains(stdout.String(), "\nnot ok") {
					t.Fatalf("run %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
				}
			})
		}
	}
}
