package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostPatternCallbackClosures(t *testing.T) {
	cli := buildSelfHostCLI(t)
	path := filepath.Join(t.TempDir(), "callbacks.fern")
	const source = `
function call(f: Option[(i32) => void]): void {
  if let Some(g) = f { let x = (): void => g(1); x(); }
}
function call_match(f: Option[(i32) => void]): void {
  match (f) {
    Some(g) => { let x = (): void => { let y = (): void => g(1); y(); }; x(); },
    None => {}
  }
}
function call_result(f: Result[(i32) => void, string]): void {
  match (f) {
    Ok(g) => { let x = (): void => g(1); x(); },
    Err(e) => { assert(false); }
  }
}
function call_value(f: Option[(i32) => i32]): void {
  if let Some(g) = f { let x = (): i32 => g(20); assert(x() == 42); }
}
function main(): i32 {
  let calls = cell_new(0);
  let f = (n: i32): void => { calls.set(calls.get() + n); };
  call(Some(f));
  call(None);
  call_match(Some(f));
  call_match(None);
  call_result(Ok(f));
  call_value(Some((n: i32): i32 => n + 22));
  assert(calls.get() == 3);
  return 0;
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	reference := buildLangBinForInterp(t)
	for _, mode := range []string{"-check", "-interp"} {
		t.Run("reference"+mode, func(t *testing.T) {
			if out, err := exec.Command(reference, mode, path).CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", mode, err, out)
			}
		})
		t.Run("self"+mode, func(t *testing.T) {
			if out, err := runX86_64Bin(cli.runner, cli.bin, mode, path, cli.stdlib).CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", mode, err, out)
			}
		})
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
	t.Run("non-void-callback-in-void-closure", func(t *testing.T) {
		const invalid = `function bad(f: Option[(i32) => i32]): void {
  if let Some(g) = f { let x = (): void => g(1); x(); }
}
function main(): i32 { return 0; }`
		if err := os.WriteFile(path, []byte(invalid), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "E002") || !strings.Contains(string(out), "expected void, got i32") {
			t.Fatalf("got %v\n%s, want a typed return refusal", err, out)
		}
	})
}
