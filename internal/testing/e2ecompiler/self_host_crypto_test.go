package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostCryptoSuitesWasm(t *testing.T) {
	wasmtime := e2eharness.Wasmtime(t)
	cli := buildSelfHostCLI(t)
	interp := buildLangBinForInterp(t)
	for _, suite := range []string{"chacha20poly1305", "aes_gcm", "aes", "des", "field25519", "x25519", "ed25519", "mlkem768", "hpke", "montgomery", "p256", "rsa", "scrypt", "argon2id", "crypto_stream", "tls_client", "tls_der", "tls_handshake", "tls_keyschedule", "tls_keyshare", "tls_message", "tls_mozilla", "tls_ocsp", "tls_record", "tls_server", "tls_identity_shared_key", "tls_verify", "tls_x509"} {
		t.Run(suite, func(t *testing.T) {
			src := langSrcAbs(t, "tests/stdlib/"+suite+"_test.fern")
			want, exit := e2eharness.InterpVerdict(t, interp, src)
			if exit != 0 {
				t.Fatalf("interpreter exit %d\n%s", exit, want)
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

func TestSelfHostX25519Iterated1000(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	prog := `import "std/hex";
import "std/codec";
import "std/crypto/x25519";
function main(): i32 {
  let k: u8[] = __codec_bytes(hex.decode("0900000000000000000000000000000000000000000000000000000000000000"));
  let u: u8[] = k;
  let i: i32 = 0;
  while (i < 1000) {
    match (x25519.x25519(k, u)) {
      Ok(r) => { u = k; k = r; },
      Err(e) => { return 1; }
    }
    i = i + 1;
  }
  print(hex.encode(k));
  return 0;
}

// Decode a valid fixture. Report a malformed fixture before ending the test.
function __codec_bytes(value: Result[u8[], codec.DecodeError]): u8[] {
  match (value) {
    Ok(bytes) => { return bytes; },
    Err(problem) => { eprint(problem.message()); exit(98); return []; }
  }
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

func TestSelfHostECDSASuite(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := langSrcAbs(t, "tests/stdlib/ecdsa_test.fern")
	check := func(t *testing.T, out []byte, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("ecdsa suite: %v\n%s", err, out)
		}
		tap := string(out)
		e2eharness.CheckTAPPlan(t, tap)
		if strings.Contains(tap, "\n# skip ") {
			t.Fatalf("ecdsa suite skipped a case:\n%s", tap)
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

// TestSelfHostScryptRFC7914 runs the two RFC 7914 vectors that the
// interpreters are too slow for. Each one fills 1 MiB or 16 MiB.
func TestSelfHostScryptRFC7914(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	prog := `import "std/hex";
import "std/crypto/scrypt";
function show(r: Result[u8[], scrypt.ScryptError]): string {
  return match (r) {
    Ok(b) => hex.encode(b),
    Err(e) => e.message(),
  };
}
function main(): i32 {
  print(show(scrypt.key("password".as_bytes(), "NaCl".as_bytes(), 1024, 8, 16, 64)));
  print(show(scrypt.key("pleaseletmein".as_bytes(), "SodiumChloride".as_bytes(), 16384, 8, 1, 64)));
  return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	want := "fdbabe1c9d3472007856e7190d01e9fe7c6ad7cbc8237830e77376634b3731622eaf30d92e22a3886ff109279d9830dac727afb94a83ee6d8360cbdfa2cc0640\n" +
		"7023bdcb3afd7348461c06cd81fd38ebfda8fbba904f8e3ea9b543f6545da1f2d5432955613f0fcf62d49705242a9af9e61e85dc0d651e40dfcf017b45575887\n"
	check := func(t *testing.T, out []byte, err error) {
		t.Helper()
		if err != nil || string(out) != want {
			t.Fatalf("scrypt RFC 7914 vectors: %v\n%s", err, out)
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

// A misuse of a crypto API that the type of a key or a context forbids is a
// compile error, and the same program without the misuse compiles.
func TestSelfHostCryptoApiRefusals(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cases := []struct {
		name, body, code string
	}{
		{"hpke context sealed twice", `let c = hpke.key_schedule(hpke.KEM_P256, __alloc_u8(32), []);
  let Ok((a, _)) = c.seal([], [1u8]) else { return 1; };
  let Ok((b, _)) = c.seal([], [2u8]) else { return 2; };
  return a.len() + b.len();`, "E051"},
		{"hpke context sealed twice from a field", `let s = hpke.Sender { enc: [], context: hpke.key_schedule(hpke.KEM_P256, __alloc_u8(32), []) };
  let Ok((a, _)) = s.context.seal([], [1u8]) else { return 1; };
  let Ok((b, _)) = s.context.seal([], [2u8]) else { return 2; };
  return a.len() + b.len();`, "E051"},
		{"hpke context built from fields", `let c = hpke.Context { key: [], base_nonce: [], seq: 0 as u64, exporter_secret: [], suite: [] };
  return 0;`, "E021"},
		{"ed25519 private key built from fields", `let k = ed25519.PrivateKey { scalar: [], expanded: [], public: [] };
  return k.sign([]).len();`, "E021"},
		{"ed25519 public key built from fields", `let Ok(k) = ed25519.private_key(__alloc_u8(32)) else { return 1; };
  let Ok(p) = ed25519.public_key(k.public_point()) else { return 2; };
  let q = ed25519.PublicKey { ...p, bytes: [] };
  return 0;`, "E021"},
		{"ed25519 montgomery_base outside the package", `return ed25519.montgomery_base(__alloc_u8(32)).len();`, "E087"},
		{"control", `let c = hpke.key_schedule(hpke.KEM_P256, __alloc_u8(32), []);
  let Ok((a, next)) = c.seal([], [1u8]) else { return 1; };
  let Ok((b, _)) = next.seal([], [2u8]) else { return 2; };
  let Ok(k) = ed25519.private_key(__alloc_u8(32)) else { return 3; };
  return a.len() + b.len() + k.sign([]).len() - 98;`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			prog := "import \"std/crypto/ed25519\";\nimport \"std/crypto/hpke\";\n\nfunction main(): i32 {\n  " + c.body + "\n}\n"
			if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := cli.compileCmd("x86-64-linux", filepath.Join(dir, "main"), src).CombinedOutput()
			if c.code == "" {
				if err != nil {
					t.Fatalf("the control does not compile: %v\n%s", err, out)
				}
				return
			}
			if err == nil {
				t.Fatalf("compiled, want %s", c.code)
			}
			if !strings.Contains(string(out), "error["+c.code+"]") {
				t.Fatalf("want %s, got:\n%s", c.code, out)
			}
		})
	}
}
