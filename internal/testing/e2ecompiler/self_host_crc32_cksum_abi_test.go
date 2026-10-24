package e2ecompiler

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostArm64Crc32CksumKeepsCalleeSavedRegisters(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeTempFern(t, t.TempDir(), "crc32",
		`function main(): i32 { return __crc32_cksum(0, "abcdefghijklmnopqrstuvwxyz0123456789"); }`)
	asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	body := kernelBody(t, string(asm), "__fern_crc32_cksum")
	bad := regexp.MustCompile(`\b[xw](1[89]|2[0-8])\b`)
	var hits []string
	for _, line := range strings.Split(body, "\n") {
		if m := bad.FindString(line); m != "" {
			hits = append(hits, strings.TrimSpace(line)+"   ("+m+")")
		}
	}
	if len(hits) > 0 {
		t.Errorf("__fern_crc32_cksum touches %d callee-saved or platform register(s) with no save/restore pair; "+
			"a caller holding a live value there is silently corrupted:\n  %s",
			len(hits), strings.Join(hits, "\n  "))
	}
}

func kernelBody(t *testing.T, asm, name string) string {
	t.Helper()
	start := strings.Index(asm, "\n"+name+":")
	if start < 0 {
		t.Fatalf("%s is not in the emitted assembly — the program did not reach the kernel", name)
	}
	rest := asm[start:]
	if end := strings.Index(rest, ".size "+name); end >= 0 {
		return rest[:end]
	}
	t.Fatalf("%s has no .size directive; cannot bound its body", name)
	return ""
}
