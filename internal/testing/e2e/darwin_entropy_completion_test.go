//go:build darwin && arm64

package e2e

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestArm64DarwinEntropyCompletion(t *testing.T) {
	const source = `import "std/i32";
function main(): i32 {
  let lengths: i32[] = [0, 1, 256, 257, 1024];
  for n in lengths {
    if (random_bytes(n).len() != n) { return 1; }
  }
  print(random_i32().to_string());
  return 0;
}`
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Darwin, source, nil)
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("Darwin entropy draw: %v\n%s", err, out)
	}
	if _, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 32); err != nil {
		t.Fatalf("random_i32 output %q: %v", out, err)
	}
}
