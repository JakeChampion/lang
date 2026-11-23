package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A function with about 160 instructions before a loop of about 150. A pool
// at each 256 instructions is in the loop.
func poolSiteProgram() string {
	var b strings.Builder
	b.WriteString("@noinline\nfunction mix(a: i64, n: i32): i64 {\n  let s = a;\n")
	for k := 0; k < 45; k++ {
		fmt.Fprintf(&b, "  s = (s ^ (a >> %di64)) + %di64;\n", k%13+1, k*7+3)
	}
	b.WriteString("  let i = 0;\n  while (i < n) {\n")
	for k := 0; k < 30; k++ {
		fmt.Fprintf(&b, "    s = (s ^ (s >> %di64)) * 6364136223846793005i64 + %di64;\n", k%11+1, k)
	}
	b.WriteString("    i += 1;\n  }\n  return s;\n}\n\n")
	b.WriteString("function main(): i32 {\n  return ((mix(7i64, 1000) % 97i64 + 97i64) % 97i64) as i32;\n}\n")
	return b.String()
}

var arm64BranchTarget = regexp.MustCompile(`^\s+(?:b|b\.\w+|cbn?z \w+,|tbn?z \w+, #\d+,) (\.L\w+)$`)

// The arm64 emitter puts a literal pool, with a branch over it, at most 256
// instructions after the pool before it. It puts the pool before the loop
// and not in it, so the loop does not run the branch on each pass.
func TestSelfHostArm64LiteralPoolOutsideLoop(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "pool.fern")
	if err := os.WriteFile(src, []byte(poolSiteProgram()), 0o644); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	body := asmFuncBody(t, string(text), "__fn_mix.r")
	if !strings.Contains(body, "_pool_") {
		t.Fatalf("mix has no literal pool in its body, so the test proves nothing:\n%s", body)
	}
	lines := strings.Split(body, "\n")
	defined := map[string]int{}
	loops := 0
	for i, line := range lines {
		if label, ok := strings.CutSuffix(line, ":"); ok {
			defined[label] = i
			continue
		}
		m := arm64BranchTarget.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		head, back := defined[m[1]]
		if !back {
			continue
		}
		loops++
		for k := head; k < i; k++ {
			if strings.TrimSpace(lines[k]) == ".ltorg" {
				t.Errorf("a literal pool is in the loop from line %d to line %d:\n%s", head, i, body)
			}
		}
	}
	if loops == 0 {
		t.Fatalf("mix has no backward branch:\n%s", body)
	}
	if stderr, code := cli.exitOfFile(t, src, "arm64-linux", nil); code != 9 {
		t.Errorf("exit %d, want 9\n%s", code, stderr)
	}
}
