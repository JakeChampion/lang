package e2ecompiler

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A manifest that names an index_key makes fern -resolve verify the
// detached ed25519 signature in index.toml.sig before it reads the
// index. Both loaders refuse the same trees with the same words.
func TestSelfHostSignedIndexResolve(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("resolve differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	const index = "[foo]\n\"1.0.0\" = { path = \"pkgs/foo-1.0.0\" }\n"
	signature := hex.EncodeToString(ed25519.Sign(priv, []byte(index))) + "\n"
	keyOf := func(k ed25519.PublicKey) string { return "ed25519:" + hex.EncodeToString(k) }
	manifest := func(key string) string {
		return "[package]\nname = \"app\"\nindex = \"index.toml\"\nindex_key = \"" + key + "\"\n[dependencies]\nfoo = \"1.0.0\"\n"
	}
	pkgs := map[string]string{
		"pkgs/foo-1.0.0/fern.toml": "[package]\nname = \"foo\"\n",
		"pkgs/foo-1.1.0/fern.toml": "[package]\nname = \"foo\"\n",
	}
	for _, c := range []struct {
		name   string
		files  map[string]string
		wantOK bool
		want   string
	}{
		{"signed", map[string]string{"fern.toml": manifest(keyOf(pub)), "index.toml": index, "index.toml.sig": signature}, true, ""},
		{"no-signature", map[string]string{"fern.toml": manifest(keyOf(pub)), "index.toml": index}, false, "fern.toml names index_key but <ROOT>/index.toml.sig cannot be read"},
		{"altered-index", map[string]string{"fern.toml": manifest(keyOf(pub)), "index.toml": index + "\"1.1.0\" = { path = \"pkgs/foo-1.1.0\" }\n", "index.toml.sig": signature}, false, "the signature in <ROOT>/index.toml.sig does not verify against index_key"},
		{"wrong-key", map[string]string{"fern.toml": manifest(keyOf(otherPub)), "index.toml": index, "index.toml.sig": signature}, false, "does not verify against index_key"},
		{"malformed-signature", map[string]string{"fern.toml": manifest(keyOf(pub)), "index.toml": index, "index.toml.sig": "not hex\n"}, false, "index <ROOT>/index.toml.sig: must be 128 hex digits of the ed25519 signature over the index"},
		{"malformed-key", map[string]string{"fern.toml": manifest("ed25519:nope"), "index.toml": index, "index.toml.sig": signature}, false, "index_key: must be `ed25519:` + 64 lowercase hex digits of the public key"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			nativeDir := filepath.Join(root, "native")
			selfDir := filepath.Join(root, "selfhost")
			for _, d := range []string{nativeDir, selfDir} {
				writeResolveProject(t, d, pkgs)
				writeResolveProject(t, d, c.files)
			}
			nativeCmd := exec.Command(nativeBin, "-resolve", nativeDir)
			nativeOut, _ := nativeCmd.CombinedOutput()
			nativeOK := nativeCmd.ProcessState.ExitCode() == 0
			shCmd := exec.Command(driverBin, "-resolve", selfDir)
			shOut, _ := shCmd.CombinedOutput()
			shOK := shCmd.ProcessState.ExitCode() == 0

			if nativeOK != c.wantOK {
				t.Fatalf("native resolved = %v, want %v\n%s", nativeOK, c.wantOK, nativeOut)
			}
			if shOK != nativeOK {
				t.Fatalf("native resolved = %v, self-host resolved = %v\n--- native ---\n%s\n--- self-host ---\n%s", nativeOK, shOK, nativeOut, shOut)
			}
			want := strings.ReplaceAll(string(nativeOut), nativeDir, "<ROOT>")
			got := strings.ReplaceAll(string(shOut), selfDir, "<ROOT>")
			if want != got {
				t.Errorf("resolve output differs:\n--- native ---\n%s\n--- self-host ---\n%s", want, got)
			}
			if c.wantOK {
				for _, d := range []string{nativeDir, selfDir} {
					if _, err := os.Stat(filepath.Join(d, "fern.lock")); err != nil {
						t.Errorf("%s: no lock written: %v", d, err)
					}
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("refusal lacks %q:\n%s", c.want, got)
			}
			for _, d := range []string{nativeDir, selfDir} {
				if _, err := os.Stat(filepath.Join(d, "fern.lock")); err == nil {
					t.Errorf("%s: a refused resolve wrote fern.lock", d)
				}
			}
		})
	}
}
