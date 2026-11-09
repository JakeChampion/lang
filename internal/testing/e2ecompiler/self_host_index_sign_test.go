package e2ecompiler

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `fern -sign-index -key KEY INDEX` writes the signature that Go's
// crypto/ed25519 gives for the same seed, and prints the index_key line. Both
// loaders then resolve the signed tree. A key that is not one PKCS#8 ed25519
// key is refused, and no signature is written. `fern -index-keygen KEY` makes
// a key that Go reads and that the sign command takes.
func TestSelfHostSignIndex(t *testing.T) {
	nativeBin, driverBin := resolveBins(t)
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i * 7)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	const index = "serial = 3\n[foo]\n\"1.0.0\" = { path = \"pkgs/foo-1.0.0\" }\n"
	keyLine := "index_key = \"ed25519:" + hex.EncodeToString(pub) + "\"\n"

	sign := func(t *testing.T, key string) (dir, stdout, stderr string, code int) {
		t.Helper()
		dir = t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "index.toml"), []byte(index), 0o644); err != nil {
			t.Fatal(err)
		}
		args := []string{"-sign-index"}
		if key != "" {
			if err := os.WriteFile(filepath.Join(dir, "key.pem"), []byte(key), 0o600); err != nil {
				t.Fatal(err)
			}
			args = append(args, "-key", filepath.Join(dir, "key.pem"))
		}
		args = append(args, filepath.Join(dir, "index.toml"))
		cmd := exec.Command(driverBin, args...)
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		_ = cmd.Run()
		return dir, out.String(), errb.String(), cmd.ProcessState.ExitCode()
	}

	t.Run("signs", func(t *testing.T) {
		dir, stdout, stderr, code := sign(t, keyPEM)
		if code != 0 || stdout != keyLine {
			t.Fatalf("exit %d, stdout %q, want 0 and %q\nstderr: %s", code, stdout, keyLine, stderr)
		}
		got, err := os.ReadFile(filepath.Join(dir, "index.toml.sig"))
		if err != nil {
			t.Fatal(err)
		}
		if want := hex.EncodeToString(ed25519.Sign(priv, []byte(index))) + "\n"; string(got) != want {
			t.Fatalf("index.toml.sig is %q, Go signs %q", got, want)
		}
		runResolveBoth(t, nativeBin, driverBin, map[string]string{"pkgs/foo-1.0.0/fern.toml": "[package]\nname = \"foo\"\n"}, resolveCase{
			name: "signed-by-fern",
			files: map[string]string{
				"fern.toml":      "[package]\nname = \"app\"\nindex = \"index.toml\"\n" + keyLine + "[dependencies]\nfoo = \"1.0.0\"\n",
				"index.toml":     index,
				"index.toml.sig": string(got),
			},
			wantOK: true,
		})
	})

	t.Run("keygen", func(t *testing.T) {
		dir := t.TempDir()
		keygen := func(path string) (string, string, int) {
			cmd := exec.Command(driverBin, "-index-keygen", path)
			var out, errb strings.Builder
			cmd.Stdout, cmd.Stderr = &out, &errb
			_ = cmd.Run()
			return out.String(), errb.String(), cmd.ProcessState.ExitCode()
		}
		var publics []string
		for _, name := range []string{"a.pem", "b.pem"} {
			path := filepath.Join(dir, name)
			stdout, stderr, code := keygen(path)
			if code != 0 {
				t.Fatalf("keygen %s: exit %d\n%s", name, code, stderr)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("%s has mode %v, want 0600", name, info.Mode().Perm())
			}
			text, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			block, _ := pem.Decode(text)
			if block == nil || block.Type != "PRIVATE KEY" {
				t.Fatalf("%s is not one PRIVATE KEY block:\n%s", name, text)
			}
			key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				t.Fatalf("Go cannot read %s: %v", name, err)
			}
			edKey, ok := key.(ed25519.PrivateKey)
			if !ok {
				t.Fatalf("%s holds a %T, want an ed25519 key", name, key)
			}
			line := "index_key = \"ed25519:" + hex.EncodeToString(edKey.Public().(ed25519.PublicKey)) + "\"\n"
			if stdout != line {
				t.Fatalf("keygen printed %q, the key is %q", stdout, line)
			}
			publics = append(publics, line)
		}
		if publics[0] == publics[1] {
			t.Fatal("two keygen runs made the same key")
		}
		before, err := os.ReadFile(filepath.Join(dir, "a.pem"))
		if err != nil {
			t.Fatal(err)
		}
		if stdout, stderr, code := keygen(filepath.Join(dir, "a.pem")); code != 1 || stdout != "" || !strings.Contains(stderr, "cannot create the key") {
			t.Fatalf("keygen over a present file: exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		if after, _ := os.ReadFile(filepath.Join(dir, "a.pem")); string(after) != string(before) {
			t.Fatal("keygen changed a present key file")
		}
		signDir, stdout, stderr, code := sign(t, string(before))
		if code != 0 || stdout != publics[0] {
			t.Fatalf("sign with the new key: exit %d, stdout %q, want %q\n%s", code, stdout, publics[0], stderr)
		}
		sig, err := os.ReadFile(filepath.Join(signDir, "index.toml.sig"))
		if err != nil {
			t.Fatal(err)
		}
		runResolveBoth(t, nativeBin, driverBin, map[string]string{"pkgs/foo-1.0.0/fern.toml": "[package]\nname = \"foo\"\n"}, resolveCase{
			name: "keygen-signed",
			files: map[string]string{
				"fern.toml":      "[package]\nname = \"app\"\nindex = \"index.toml\"\n" + publics[0] + "[dependencies]\nfoo = \"1.0.0\"\n",
				"index.toml":     index,
				"index.toml.sig": string(sig),
			},
			wantOK: true,
		})
	})

	x25519, err := ecdh.X25519().NewPrivateKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	otherDER, err := x509.MarshalPKCS8PrivateKey(x25519)
	if err != nil {
		t.Fatal(err)
	}
	otherPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: otherDER}))
	for _, c := range []struct{ name, key, want string }{
		{"no-key", "", "fern: -sign-index needs -key FILE, the ed25519 private key in PKCS#8 PEM\n"},
		{"not-pem", "not a key\n", "the key must hold one PRIVATE KEY block, found 0"},
		{"x25519-key", otherPEM, "the PRIVATE KEY block is not a PKCS#8 ed25519 key"},
		{"two-keys", keyPEM + keyPEM, "the key must hold one PRIVATE KEY block, found 2"},
		{"truncated-der", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der[:40]})), "the PRIVATE KEY block is not a PKCS#8 ed25519 key"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir, stdout, stderr, code := sign(t, c.key)
			if code != 1 || stdout != "" || !strings.Contains(stderr, c.want) {
				t.Fatalf("exit %d, stdout %q, stderr %q, want 1 and %q", code, stdout, stderr, c.want)
			}
			if _, err := os.Stat(filepath.Join(dir, "index.toml.sig")); err == nil {
				t.Fatal("a refused key wrote index.toml.sig")
			}
		})
	}
}
