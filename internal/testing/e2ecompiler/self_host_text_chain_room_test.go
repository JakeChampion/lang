package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The first `+` of a chain of texts makes a text with room for the appends
// after it, so a short chain allocates one time. A text that the program
// keeps while the chain continues must not change.
func TestSelfHostTextChainAllocatesOneTime(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(textChainRoomSource), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	// The program runs on this machine, so the target is the host.
	cmd := exec.Command(cli, "-target", e2eharness.HostSelfHostTarget(), "-o", out, src, stdlib)
	cmd.Env = append(os.Environ(), "FERN_SANITIZE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, output)
	}
	output, err := exec.Command(out).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	got := string(output)
	if !strings.HasPrefix(got, textChainRoomWant) {
		t.Fatalf("output:\n%s\nwant:\n%s", got, textChainRoomWant)
	}
	if rest := strings.TrimPrefix(got, textChainRoomWant); !strings.Contains(rest, "live_bytes=0") || strings.Count(rest, "\n") != 1 {
		t.Fatalf("the sanitizer reported:\n%s", rest)
	}
}

const textChainRoomSource = `import "std/string";

@noinline function mov(a: string, b: string): string {
  return "    movq " + a + ", " + b + ";";
}

@noinline function kept(x: string): string {
  let head = "id=" + x;
  let full = head + "!";
  return full + " " + head;
}

function main(): i32 {
  let mark = __heap_alloc_count();
  let total = 0;
  for k in 0..1000 {
    total += mov("%rax", "%rbx").len();
  }
  let chain = __heap_alloc_count() - mark;
  print("chain " + chain.to_string() + " " + total.to_string());
  print(mov("%r8", "x".repeat(70)));
  print(kept("seven"));
  return 0;
}
`

const textChainRoomWant = "chain 1000 20000\n    movq %r8, " +
	"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx;\n" +
	"id=seven! id=seven\n"
