package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOracleNamesItselfFernOracle(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fern-oracle")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	src := filepath.Join(dir, "a.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"usage", nil, "usage: fern-oracle [-target"},
		{"undefined flag", []string{"-lsp"}, "usage: fern-oracle [-target"},
		{"cover with interp", []string{"-cover", "-interp", src}, "fern-oracle: -cover instruments compiled code"},
		{"fmt -o with two files", []string{"-fmt", "-o", filepath.Join(dir, "out.fern"), src, src}, "fern-oracle: -fmt -o takes a single input file"},
		{"flag after file", []string{"-interp", src, "-o", "x"}, "fern-oracle: -o comes after the source file"},
		{"add refusal", []string{"-add", "9bad", "path:../x", dir}, "fern-oracle: -add: invalid dependency name"},
		{"grant after add's arguments", []string{"-add", "kv", "path:../kv", dir, "-grant", "net"}, "fern-oracle: -grant comes after -add's arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := exec.Command(bin, tc.argv...).CombinedOutput()
			if err == nil {
				t.Fatalf("fern-oracle %s succeeded; want a refusal\n%s", strings.Join(tc.argv, " "), out)
			}
			text := string(out)
			if !strings.Contains(text, tc.want) {
				t.Errorf("fern-oracle %s does not print %q:\n%s", strings.Join(tc.argv, " "), tc.want, text)
			}
			if strings.Contains(text, "usage: fern [") || strings.Contains(text, "\nfern: ") || strings.HasPrefix(text, "fern: ") {
				t.Errorf("fern-oracle %s calls itself fern:\n%s", strings.Join(tc.argv, " "), text)
			}
		})
	}
	out, err := exec.Command(bin, "-version").Output()
	if err != nil {
		t.Fatalf("fern-oracle -version: %v", err)
	}
	if !strings.HasPrefix(string(out), "fern-oracle ") {
		t.Errorf("-version does not name the oracle:\n%s", out)
	}
}
