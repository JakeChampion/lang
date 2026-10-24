package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const laneLateParamSrc = `import "std/f64";

@noinline function late(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, v: f64x2): f64 {
  return __f64x2_lane(v, 1) + ((a + h) as f64);
}

function main(): i32 {
  print(late(1, 2, 3, 4, 5, 6, 7, 8, __f64x2_splat(0.5)).to_string());
  return 0;
}
`

func TestSelfHostLaneParamPastRegisters(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(entry, []byte(laneLateParamSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux} {
		cmd := e2eharness.SelfHostCompileCmd(t, target, entry, filepath.Join(dir, "prog-"+target))
		cmd.Env = e2eharness.SelfHostChildEnv()
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "a lane value crosses a call only as one of the first") {
			t.Errorf("%s: want the lane-call refusal, got err %v\n%s", target, err, out)
		}
	}
	bin := filepath.Join(dir, "main.wasm")
	cmd := e2eharness.SelfHostCompileCmd(t, e2eharness.TargetWasm32Wasi, entry, bin)
	cmd.Env = e2eharness.SelfHostChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wasm compile: %v\n%s", err, out)
	}
	if out, err := e2eharness.RunWasmCore(t, bin).CombinedOutput(); err != nil || string(out) != "9.5\n" {
		t.Fatalf("wasm run: %v, output %q, want %q", err, out, "9.5\n")
	}
}
