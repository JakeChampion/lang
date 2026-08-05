package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/embed"
)

// writeAssets materialises an asset tree and loads it the way the -embed
// flag does, leaving it installed as the compile-time asset set for the
// duration of the test.
func writeAssets(t *testing.T, files map[string]string) {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, err := embed.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	prev := embeddedAssets
	embeddedAssets = set
	t.Cleanup(func() { embeddedAssets = prev })
}

// A compile-time asset reaches the running binary byte-for-byte. The blob
// is the load-bearing half: it carries interior NULs and bytes >= 0x80, so
// it only survives because the emitted literal has an explicit length at
// data-4 (the .asciz terminator is not the length) and because escapeForGAS
// round-trips high bytes rather than re-encoding them as UTF-8.
func TestEmbedAssetReachesTheBinary(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skipf("needs native linux/amd64 execution, host is %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	blob := string([]byte{0x00, 0x01, 0x80, 0xff, 0x00, 'e', 'n', 'd'}) // 8 bytes, two interior NULs
	writeAssets(t, map[string]string{
		"html/index.html": "<h1>embedded</h1>",
		"blob.bin":        blob,
	})

	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	prog := `const PAGE: string = __fern_asset("html/index.html");

function main(): i32 {
    print(PAGE);
    var b: string = __fern_asset("blob.bin");
    return b.len() as i32;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	code, err := run(src, bin, "x86-64", "", false, false, "qemu-aarch64",
		false, false, false, nil, false, "", false, nil)
	if err != nil || code != 0 {
		t.Fatalf("build: code=%d err=%v", code, err)
	}

	cmd := exec.Command(bin)
	out, _ := cmd.CombinedOutput()
	if got := strings.TrimSpace(string(out)); got != "<h1>embedded</h1>" {
		t.Errorf("stdout = %q, want the embedded page", got)
	}
	if got := cmd.ProcessState.ExitCode(); got != len(blob) {
		t.Errorf("binary asset length = %d, want %d — interior NUL truncated the literal", got, len(blob))
	}
}

// Without -embed, __fern_asset is a compile error naming the flag, not an
// "undefined identifier" from the checker.
func TestEmbedMissingFlagIsADiagnostic(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	prog := `function main(): i32 {
    var s: string = __fern_asset("a.txt");
    return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := embeddedAssets
	embeddedAssets = nil
	t.Cleanup(func() { embeddedAssets = prev })

	err := runCheck(src)
	if err == nil {
		t.Fatal("expected -check to reject __fern_asset with no -embed")
	}
	if !strings.Contains(err.Error(), "-embed") {
		t.Fatalf("error %q should point at the -embed flag", err)
	}
}

// A misspelled asset names the near miss rather than only failing.
func TestEmbedUnknownAssetSuggests(t *testing.T) {
	writeAssets(t, map[string]string{"html/index.html": "x"})
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	prog := `function main(): i32 {
    var s: string = __fern_asset("html/index.htm");
    return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runCheck(src)
	if err == nil {
		t.Fatal("expected -check to reject an unknown asset")
	}
	if !strings.Contains(err.Error(), `did you mean "html/index.html"`) {
		t.Fatalf("error %q should suggest the near miss", err)
	}
}
