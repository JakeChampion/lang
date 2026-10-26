package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A branch to a block that emits no code and only jumps on takes the final
// target. The DEFLATE encoder had such branches on its hot paths.
func TestSelfHostSSABranchForwarding(t *testing.T) {
	const program = `import "std/deflate";
function main(): i32 {
  let text: u8[] = [];
  let i: i32 = 0;
  while (i < 4000) {
    text = text.append((97 + i % 7 + i / 300 % 3) as u8);
    i = i + 1;
  }
  let packed: u8[] = deflate.deflate(text);
  let back: u8[] = match (deflate.inflate(packed, text.len())) { Ok(v) => v.out, Err(_) => { return 1; } };
  if (back.len() != text.len() || packed.len() >= text.len()) { return 2; }
  for k in 0..text.len() {
    if (back[k] != text[k]) { return 3; }
  }
  return 0;
}`
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, program, target)
			if code != 0 {
				t.Fatalf("deflate round trip: exit %d, want 0\n%s", code, stderr)
			}
			src := filepath.Join(t.TempDir(), "forward.fern")
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			assembly, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range jumpsToJumps(string(assembly)) {
				t.Errorf("branch to a block that only jumps on: %s", b)
			}
		})
	}
}

var (
	ssaLabel    = regexp.MustCompile(`^(\.Lssa_\S+):`)
	blockBranch = regexp.MustCompile(`^\s+(?:j[a-z]+|b(?:\.[a-z]+)?|cbn?z|tbn?z)\s+(?:\S+,\s*)*(\.Lssa_\S+_\d+)\s*$`)
	blockJump   = regexp.MustCompile(`^\s+(?:jmp|b)\s+\.Lssa_\S+_\d+\s*$`)
)

// The branches whose target block starts with an unconditional jump to a block.
func jumpsToJumps(assembly string) []string {
	lines := strings.Split(assembly, "\n")
	first := map[string]string{}
	label := ""
	for _, line := range lines {
		if m := ssaLabel.FindStringSubmatch(line); m != nil {
			label = m[1]
			continue
		}
		code := strings.TrimSpace(line)
		if label != "" && code != "" && !strings.HasPrefix(code, ".") {
			first[label] = line
			label = ""
		}
	}
	var bad []string
	for _, line := range lines {
		m := blockBranch.FindStringSubmatch(line)
		if m != nil && blockJump.MatchString(first[m[1]]) {
			bad = append(bad, strings.TrimSpace(line)+" -> "+strings.TrimSpace(first[m[1]]))
		}
	}
	return bad
}
