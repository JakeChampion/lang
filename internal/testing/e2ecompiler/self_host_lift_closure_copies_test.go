package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// closuresProgram is one function with n closures. Each closure captures a
// parameter, and a statement of the function calls it.
func closuresProgram(n int) string {
	var b strings.Builder
	b.WriteString("function run(seed: i32): i32 {\n  let t: i32 = seed;\n")
	for k := 0; k < n; k++ {
		fmt.Fprintf(&b, "  let g%d = (x: i32): i32 => x + %d + seed;\n", k, k)
		fmt.Fprintf(&b, "  if (t > %d) { t = t - g%d(t); } else { t = t + g%d(t); }\n", k, k, k)
	}
	b.WriteString("  return t;\n}\nfunction main(): i32 { return run(1) & 1; }\n")
	return b.String()
}

var gatedBytesRe = regexp.MustCompile(`(?m)^sem:gated: arr_push_shared_count=\d+ arr_push_shared_bytes=(\d+)`)

// gatedCopiedBytes is the number of bytes that the self-host compiler copies
// in appends to a shared array before the gated phase, where it lifts lambdas.
func gatedCopiedBytes(t *testing.T, h ssaBackendHost, src string) int {
	t.Helper()
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-o", filepath.Join(t.TempDir(), "out"), src, h.stdlib)
	cmd.Env = append(os.Environ(), "FERN_CLIFF_REPORT=1", "FERN_RC_CERTIFY=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compiling %s: %v\n%s", src, err, out)
	}
	m := gatedBytesRe.FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("no cliff report for the gated phase:\n%s", out)
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// The names that a lambda reads go into the list of the caller in place. A
// copy of that list for each lambda made the copied bytes grow with the cube
// of the number of closures: 62 MB at 100 closures and 489 MB at 200 (#14578).
func TestSelfHostLambdaReadsAppendInPlace(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	bytes := map[int]int{}
	for _, n := range []int{100, 200} {
		src := filepath.Join(dir, fmt.Sprintf("closures%d.fern", n))
		if err := os.WriteFile(src, []byte(closuresProgram(n)), 0o644); err != nil {
			t.Fatal(err)
		}
		bytes[n] = gatedCopiedBytes(t, h, src)
	}
	t.Logf("copied bytes: %d at 100 closures, %d at 200", bytes[100], bytes[200])
	if bytes[200] > 6*bytes[100] {
		t.Errorf("two times the closures copy %d bytes in place of %d, more than six times", bytes[200], bytes[100])
	}
	if bytes[200] > 50<<20 {
		t.Errorf("200 closures copy %d bytes, more than 50 MiB", bytes[200])
	}
}
