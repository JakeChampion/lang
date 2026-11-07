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

// spliceProgram is one function that calls a small leaf n times. The inliner
// splices each call into the caller, so it grows the caller's value table n
// times.
func spliceProgram(n int) string {
	var b strings.Builder
	b.WriteString("@noinline function sink(x: i32): i32 { return x; }\n")
	b.WriteString("function bump(x: i32): i32 { return sink(x + 1); }\n")
	b.WriteString("function many(a: i32): i32 {\n  let t = 0;\n")
	for k := 0; k < n; k++ {
		fmt.Fprintf(&b, "  t += bump(a + %d);\n", k)
	}
	b.WriteString("  return t;\n}\nfunction main(): i32 { return many(1) & 1; }\n")
	return b.String()
}

var phaseCountRe = regexp.MustCompile(`(?m)^sem:(gated|built): arr_push_shared_count=(\d+)`)

// builtCopies is the number of appends that copy a shared array between the
// gated and the built phase of the self-host compiler, where it splices.
func builtCopies(t *testing.T, h ssaBackendHost, src string) int {
	t.Helper()
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-o", filepath.Join(t.TempDir(), "out"), src, h.stdlib)
	cmd.Env = append(os.Environ(), "FERN_CLIFF_REPORT=1", "FERN_RC_CERTIFY=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compiling %s: %v\n%s", src, err, out)
	}
	at := map[string]int{}
	for _, m := range phaseCountRe.FindAllStringSubmatch(string(out), -1) {
		if _, seen := at[m[1]]; !seen {
			at[m[1]], _ = strconv.Atoi(m[2])
		}
	}
	gated, g := at["gated"]
	built, b := at["built"]
	if !g || !b {
		t.Fatalf("no cliff report for the gated and built phases:\n%s", out)
	}
	return built - gated
}

// Each splice grows the caller's value table in place, so the copies do not
// grow with the number of splices (#14179).
func TestSelfHostSpliceGrowsValuesInPlace(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	copies := map[int]int{}
	for _, n := range []int{16, 64} {
		src := filepath.Join(dir, fmt.Sprintf("splice%d.fern", n))
		if err := os.WriteFile(src, []byte(spliceProgram(n)), 0o644); err != nil {
			t.Fatal(err)
		}
		copies[n] = builtCopies(t, h, src)
	}
	if copies[64] != copies[16] {
		t.Fatalf("splice copies grow with the number of splices: %d for 16 calls, %d for 64", copies[16], copies[64])
	}
}
