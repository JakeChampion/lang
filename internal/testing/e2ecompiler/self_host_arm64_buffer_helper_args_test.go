package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostArm64BufferHelperArguments(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := copySelfHostTree(t)
	const source = `import "./x86_native";
function main(): i32 {
  let a = x86_native.x86_gas_assemble("vpsrlw $3, %ymm1, %ymm2\n");
  if (a.unknown.len() != 0 || a.text.len() != 5) { return 1; }
  let want = [197, 237, 113, 209, 3];
  let i = 0;
  while (i < want.len()) {
    if (a.text[i] as i32 != want[i]) { return 2; }
    i = i + 1;
  }
  return 42;
}`
	entry := filepath.Join(dir, "buffer_helper_args.fern")
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			var output []byte
			var code int
			if target == "arm64-linux" {
				_, qemu := arm64Tooling(t)
				cmd := runArm64Bin(qemu, cli.arm64Binary(t, entry))
				output, _ = cmd.CombinedOutput()
				if cmd.ProcessState == nil {
					t.Fatalf("no process state: %s", output)
				}
				code = cmd.ProcessState.ExitCode()
			} else {
				cmd := runX86_64Bin(cli.runner, cli.x86Binary(t, entry))
				output, _ = cmd.CombinedOutput()
				if cmd.ProcessState == nil {
					t.Fatalf("no process state: %s", output)
				}
				code = cmd.ProcessState.ExitCode()
			}
			if code != 42 {
				t.Fatalf("encoder exit %d, want 42 (1: shape, 2: bytes)\n%s", code, output)
			}
		})
	}
}
