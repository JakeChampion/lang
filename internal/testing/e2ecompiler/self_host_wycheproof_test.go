package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run the larger Wycheproof and ACVP suites on the native targets. The oracle alone
// takes about 25 minutes for the P-256 signature vectors.
func TestSelfHostWycheproofNative(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, filename := range []string{
		"acvp_mldsa65_test.fern",
		"wycheproof_ecdh_p256_test.fern",
		"wycheproof_ecdsa_p256_test.fern",
		"wycheproof_ecdsa_p256_sha512_test.fern",
		"wycheproof_ecdsa_p384_sha384_test.fern",
		"wycheproof_hkdf_sha256_test.fern",
		"wycheproof_hmac_sha256_test.fern",
		"wycheproof_mldsa65_test.fern",
		"wycheproof_mlkem768_test.fern",
		"wycheproof_mlkem768_decaps_test.fern",
		"wycheproof_mlkem768_encaps_test.fern",
		"wycheproof_mlkem768_keygen_test.fern",
		"wycheproof_rsa_oaep_test.fern",
		"wycheproof_rsa_pkcs1v15_test.fern",
		"wycheproof_rsa_pss_test.fern",
		"wycheproof_xchacha20poly1305_test.fern",
	} {
		src := langSrcAbs(t, "tests/stdlib/"+filename)
		suite := strings.TrimSuffix(strings.TrimPrefix(filename, "wycheproof_"), "_test.fern")
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
