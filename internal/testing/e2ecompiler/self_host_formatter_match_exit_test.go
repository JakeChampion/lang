package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelfHostFormatterDivergentMatchArms(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const source = `function choose(ok: boolean): i32 {
  let text: string = match (ok) {
    true => "ok",
    false => { eprint(""); return 7; }
  };
  return text.len();
}
function main(): i32 {
  if (choose(true) != 2 || choose(false) != 7) { return 1; }
  return 0;
}`
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := runX86_64Bin(cli.runner, cli.bin, "-fmt", path).CombinedOutput()
	if err != nil {
		t.Fatalf("format: %v\n%s", err, first)
	}
	if err := os.WriteFile(path, first, 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := runX86_64Bin(cli.runner, cli.bin, "-fmt", path).CombinedOutput()
	if err != nil || string(first) != string(second) {
		t.Fatalf("format is not stable: %v\nfirst:\n%s\nsecond:\n%s", err, first, second)
	}
	if out, err := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("formatted source: %v\n%s", err, out)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, string(first), target, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("formatted match: exit %d\n%s", code, stderr)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil || allocs != frees || live != 0 {
				t.Fatalf("formatted match census: %v\n%s", err, stderr)
			}
		})
	}
}
