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
			for _, b := range jumpsToFallthrough(string(assembly)) {
				t.Errorf("branch to its physical fallthrough: %s", b)
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
	for i, line := range lines {
		if m := ssaLabel.FindStringSubmatch(line); m != nil {
			label = m[1]
			continue
		}
		code := strings.TrimSpace(line)
		if label != "" && code != "" && !strings.HasPrefix(code, ".") {
			if !blockJump.MatchString(line) || i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) != ".ltorg" {
				first[label] = line
			}
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

func jumpsToFallthrough(assembly string) []string {
	lines := strings.Split(assembly, "\n")
	var bad []string
	for i, line := range lines {
		if !blockJump.MatchString(line) {
			continue
		}
		target := blockBranch.FindStringSubmatch(line)[1]
		for _, next := range lines[i+1:] {
			if label := ssaLabel.FindStringSubmatch(next); label != nil {
				if label[1] == target {
					bad = append(bad, strings.TrimSpace(line))
					break
				}
				continue
			}
			code := strings.TrimSpace(next)
			if code == "" || strings.HasPrefix(code, ".cfi_") {
				continue
			}
			break
		}
	}
	return bad
}

func TestSSABranchForwardingFallthrough(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"next-label", "    b .Lssa_f_1\n.Lssa_f_1:\n", 1},
		{"alias-label", "    jmp .Lssa_f_1\n.Lssa_f_2:\n.Lssa_f_1:\n", 1},
		{"cfi", "    b .Lssa_f_1\n    .cfi_remember_state\n.Lssa_f_1:\n", 1},
		{"phi-move", "    b .Lssa_f_1\n.Lssa_f_2:\n    mov x0, x1\n.Lssa_f_1:\n", 0},
		{"literal-pool", "    b .Lssa_f_1\n    .ltorg\n.Lssa_f_1:\n", 0},
		{"conditional", "    b.eq .Lssa_f_1\n.Lssa_f_1:\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := jumpsToFallthrough(tc.body); len(got) != tc.want {
				t.Fatalf("fallthrough branches = %v, want %d", got, tc.want)
			}
		})
	}
}

func TestSSABranchForwardingLiteralPool(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"empty-block", "    b .Lssa_f_2\n", 1},
		{"literal-pool", "    b .Lssa_f_pool_2\n    .ltorg\n.Lssa_f_pool_2:\n    add x0, x0, #1\n", 0},
		{"pool-after-code", "    add x0, x0, #1\n    b .Lssa_f_pool_2\n    .ltorg\n.Lssa_f_pool_2:\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := "    b .Lssa_f_1\n.Lssa_f_1:\n" + tc.body
			if got := jumpsToJumps(asm); len(got) != tc.want {
				t.Fatalf("redundant branches = %v, want %d", got, tc.want)
			}
		})
	}
}
