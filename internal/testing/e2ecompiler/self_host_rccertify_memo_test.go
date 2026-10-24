package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var memoLine = regexp.MustCompile(`FERN_RC_CERTIFY: memo hits=(\d+) misses=(\d+)`)

func compileCapturing(t *testing.T, cli *selfHostCLI, src string, env ...string) (string, int) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.s")
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, cli.stdlib)
	cmd.Env = append(os.Environ(), env...)
	msg, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("self-host CLI: %v\n%s", err, msg)
		}
		code = ee.ExitCode()
	}
	return string(msg), code
}

func memoCounts(t *testing.T, out string) (int, int) {
	t.Helper()
	m := memoLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no memo line in the compiler's output:\n%s", out)
	}
	hits, _ := strconv.Atoi(m[1])
	misses, _ := strconv.Atoi(m[2])
	return hits, misses
}

func TestSelfHostRcCertifyMemo(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	memo := "FERN_RC_CERTIFY_MEMO=" + filepath.Join(dir, "memo")
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(sanSelfHostCleanSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := compileCapturing(t, cli, src, memo, "FERN_IR_VERIFY=1")
	if code != 0 {
		t.Fatalf("first compile: exit %d\n%s", code, out)
	}
	hits, misses := memoCounts(t, out)
	if hits != 0 || misses == 0 {
		t.Fatalf("a fresh memo: hits=%d misses=%d, want no hits and some misses", hits, misses)
	}
	out, code = compileCapturing(t, cli, src, memo, "FERN_IR_VERIFY=1")
	if code != 0 {
		t.Fatalf("second compile: exit %d\n%s", code, out)
	}
	hits2, misses2 := memoCounts(t, out)
	if misses2 != 0 || hits2 != misses {
		t.Fatalf("a warm memo: hits=%d misses=%d, want %d hits and no misses", hits2, misses2, misses)
	}

	broken := filepath.Join(dir, "broken.fern")
	if err := os.WriteFile(broken, []byte(uafSelfHostIncSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = compileCapturing(t, cli, broken, memo, "FERN_IR_VERIFY=")
	if code != 5 || !strings.Contains(out, "FERN_RC_CERTIFY: main emits unbalanced reference counting") {
		t.Fatalf("planted imbalance: exit %d, want 5 naming main\n%s", code, out)
	}
	out, code = compileCapturing(t, cli, broken, memo, "FERN_IR_VERIFY=")
	if code != 5 {
		t.Fatalf("planted imbalance after a refusal: exit %d, want 5 again (a refused function is not memoised)\n%s", code, out)
	}

	out, code = compileCapturing(t, cli, src, "FERN_RC_CERTIFY_MEMO=0", "FERN_IR_VERIFY=1")
	if code != 0 || memoLine.MatchString(out) {
		t.Fatalf("memo off: exit %d, output %q, want a clean compile with no memo line", code, out)
	}
	out, code = compileCapturing(t, cli, broken, memo, "FERN_IR_VERIFY=", "FERN_RC_CERTIFY=0")
	if code != 0 {
		t.Fatalf("FERN_RC_CERTIFY=0: exit %d, want 0\n%s", code, out)
	}
}
