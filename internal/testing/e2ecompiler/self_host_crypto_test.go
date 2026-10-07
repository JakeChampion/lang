package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The wasm leg of the std/crypto suites, which the stdtest differential runs
// on x86-64 and arm64: each component's TAP output must match the
// interpreter's byte for byte.
func TestSelfHostCryptoSuitesWasm(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	cli := buildSelfHostCLI(t)
	interp := buildLangBinForInterp(t)
	for _, suite := range []string{"chacha20poly1305", "x25519", "ed25519", "mlkem768", "rsa"} {
		t.Run(suite, func(t *testing.T) {
			src := langSrcAbs(t, "tests/stdlib/"+suite+"_test.fern")
			want, err := exec.Command(interp, "-interp", src).Output()
			if err != nil {
				t.Fatalf("interpreter: %v\n%s", err, want)
			}
			got, err := exec.Command(wasmtime, "run", cli.wasmComponent(t, src, "FERN_STRICT_IR=1")).Output()
			if err != nil {
				t.Fatalf("wasmtime run: %v\n%s", err, got)
			}
			if string(got) != string(want) {
				t.Errorf("TAP output mismatch:\n--- wasm ---\n%s\n--- interp ---\n%s", got, want)
			}
		})
	}
}

// RFC 7748 §5.2's iterated vector at 1,000 steps, compiled: too slow for the
// interpreter suite, and the strongest single check of the field arithmetic.
func TestSelfHostX25519Iterated1000(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	prog := `import "std/hex";
import "std/crypto/x25519";
function main(): i32 {
  let k: u8[] = hex.hex_decode("0900000000000000000000000000000000000000000000000000000000000000");
  let u: u8[] = k;
  let i: i32 = 0;
  while (i < 1000) {
    match (x25519.x25519(k, u)) {
      Ok(r) => { u = k; k = r; },
      Err(e) => { return 1; }
    }
    i = i + 1;
  }
  print(hex.hex_encode(k));
  return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.x86Binary(t, src, "FERN_STRICT_IR=1")).Output()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if got, want := string(out), "684cf59ba83309552800ef566f2f4d3c1c3887c49360e3875f2eb94d99532c51\n"; got != want {
		t.Errorf("1,000 iterations gave %q, want %q", got, want)
	}
}

// The std/crypto/p256 suite, compiled on all three targets. Its verifications
// run on core/bigint and take the interpreter tens of seconds, so it is not a
// stdtest differential case; each target must pass every case instead.
func TestSelfHostP256Suite(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := langSrcAbs(t, "tests/stdlib/p256_test.fern")
	check := func(t *testing.T, out []byte, err error) {
		t.Helper()
		if err != nil || !strings.Contains(string(out), "# pass 4\n# fail 0\n") {
			t.Fatalf("p256 suite: %v\n%s", err, out)
		}
	}
	t.Run("x86-64", func(t *testing.T) {
		out, err := runX86_64Bin(cli.runner, cli.x86Binary(t, src, "FERN_STRICT_IR=1")).Output()
		check(t, out, err)
	})
	t.Run("arm64", func(t *testing.T) {
		_, qemu := arm64Tooling(t)
		out, err := runArm64Bin(qemu, cli.arm64Binary(t, src, "FERN_STRICT_IR=1")).Output()
		check(t, out, err)
	})
	t.Run("wasm", func(t *testing.T) {
		out, err := exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, src, "FERN_STRICT_IR=1")).Output()
		check(t, out, err)
	})
}
