package e2ecompiler

import (
	"bytes"
	"os/exec"
	"testing"
)

// A method on a tuple receiver is called under the tuple namespace,
// `tuple2.sum`. A body with one block is spliced into its caller, so the
// name is never looked up. A body with a branch stays a call, and the
// emitters must define it under the same name.
const tupleReceiverLabelProg = `@noinline
function flag(): boolean {
  return true;
}

function (t: (i32, i32)) sum(): i32 {
  if (flag()) {
    return t.0 + t.1;
  }
  return t.0 - t.1;
}

function (t: (i32, string, boolean)) tag(): string {
  if (t.2) {
    return t.1 + t.0.to_string();
  }
  return t.1;
}

function main(): i32 {
  let p: (i32, i32) = (1, 2);
  let q: (i32, string, boolean) = (7, "q", true);
  print(p.sum().to_string() + " " + q.tag());
  return 0;
}
`

func TestSelfHostTupleReceiverMethodWithBranch(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			bin := compileCPUProfile(t, cli, tupleReceiverLabelProg, target, "")
			var cmd *exec.Cmd
			switch target {
			case "x86-64-linux":
				cmd = runX86_64Bin(cli.runner, bin)
			case "arm64-linux":
				_, qemu := arm64Tooling(t)
				cmd = runArm64Bin(qemu, bin)
			default:
				cmd = exec.Command("wasmtime", "run", bin)
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("%v: stdout %q; stderr %s", err, &stdout, &stderr)
			}
			if got := stdout.String(); got != "3 q7\n" {
				t.Fatalf("output %q, want %q", got, "3 q7\n")
			}
		})
	}
}
